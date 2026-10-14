package cnpg

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// Image volumes need extension_control_path, added in PostgreSQL 18.
const imageVolumePostgresMajor = "18"

// SQLVersionAnnotationPrefix keys, on the extensions ClusterImageCatalog, the
// CREATE EXTENSION version of each image as <prefix><major>.<extension name>;
// ExtensionConfiguration has no field for it.
const SQLVersionAnnotationPrefix = "sql-version.extensions.openeverest.io/"

// extension is a first-class toggle under parameters.extensions. Image-volume
// extensions are resolved by name from the extensions ClusterImageCatalog
// rendered by the Helm chart (values extensionCatalog), so admins can change
// or mirror images without upgrading the provider.
type extension struct {
	field  string
	toggle func(*components.ExtensionsSpec) *components.ExtensionToggle
	// imageName is the catalog extension entry; empty when the standard operand
	// images already ship the extension.
	imageName string
	// sqlName is created in the application database; empty when there is no
	// CREATE EXTENSION or the operator manages it.
	sqlName          string
	preloadLibraries []string
	// defaultParameters apply unless set in parameters.postgresql.parameters.
	defaultParameters map[string]string
	requires          []string
	// bootstrapSQL runs as the superuser in the application database at initdb,
	// so such extensions can only be enabled when the instance is created.
	bootstrapSQL func(owner string) string
}

// Ordered so that dependencies are created first (postgis before pgrouting).
var firstClassExtensions = []extension{
	{
		field:            "timescaledb",
		toggle:           func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.TimescaleDB },
		imageName:        "timescaledb-oss",
		sqlName:          "timescaledb",
		preloadLibraries: []string{"timescaledb"},
		// From the upstream timescaledb-oss Cluster example.
		defaultParameters: map[string]string{
			"timescaledb.telemetry_level": "off",
			"max_locks_per_transaction":   "128",
		},
	},
	{
		field:     "postgis",
		toggle:    func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PostGIS },
		imageName: "postgis",
		sqlName:   "postgis",
	},
	{
		field:     "pgrouting",
		toggle:    func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgRouting },
		imageName: "pgrouting",
		sqlName:   "pgrouting",
		requires:  []string{"postgis"},
	},
	{
		field:   "pgvector",
		toggle:  func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgVector },
		sqlName: "vector",
	},
	{
		field:            "pgivm",
		toggle:           func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgIVM },
		imageName:        "pg-ivm",
		sqlName:          "pg_ivm",
		preloadLibraries: []string{"pg_ivm"},
	},
	{
		// The operator preloads pgaudit and creates it in every database while a
		// pgaudit.* parameter is set, and drops it otherwise, so it must not be
		// created through the Database resource.
		field:             "pgaudit",
		toggle:            func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgAudit },
		defaultParameters: map[string]string{"pgaudit.log": "ddl, role"},
	},
	{
		field:     "wal2json",
		toggle:    func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.Wal2JSON },
		imageName: "wal2json",
	},
	{
		field:     "age",
		toggle:    func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.AGE },
		imageName: "age",
		sqlName:   "age",
		// Non-superusers cannot LOAD it from an image volume (only $libdir/plugins).
		preloadLibraries: []string{"age"},
		// CREATE EXTENSION creates ag_catalog as the superuser, and AGE refuses a
		// pre-created one, so grant the owner USAGE on schemas created later.
		bootstrapSQL: func(owner string) string {
			return "ALTER DEFAULT PRIVILEGES FOR ROLE postgres GRANT USAGE ON SCHEMAS TO " + quoteIdentifier(owner)
		},
	},
}

func (e extension) enabled(custom *components.CNPGCustomSpec) bool {
	if custom == nil || custom.Extensions == nil {
		return false
	}
	toggle := e.toggle(custom.Extensions)
	return toggle != nil && toggle.Enabled
}

func enabledExtensions(custom *components.CNPGCustomSpec) []extension {
	var enabled []extension
	for _, ext := range firstClassExtensions {
		if ext.enabled(custom) {
			enabled = append(enabled, ext)
		}
	}
	return enabled
}

// ValidateExtensions checks that image-volume extensions run on PostgreSQL 18
// and that dependencies are enabled too. ImageVolume support cannot be probed
// here; the cluster must run Kubernetes 1.35+ (or 1.33/1.34 with the
// ImageVolume feature gate).
func ValidateExtensions(custom *components.CNPGCustomSpec, engineVersion, instanceVersion string) error {
	major := postgresMajor(engineVersion, instanceVersion)
	enabled := enabledExtensions(custom)
	for _, ext := range enabled {
		if ext.imageName != "" && major != imageVolumePostgresMajor {
			return fmt.Errorf(
				"extensions.%s requires PostgreSQL 18 (got major %q); set spec.version to a 18.x release (e.g. \"18.4\"); also requires Kubernetes ImageVolume (1.35+, or 1.33/1.34 with the ImageVolume feature gate)",
				ext.field, major,
			)
		}
		for _, required := range ext.requires {
			if !slices.ContainsFunc(enabled, func(e extension) bool { return e.field == required }) {
				return fmt.Errorf("extensions.%s requires extensions.%s", ext.field, required)
			}
		}
	}
	return nil
}

func postgresMajor(engineVersion, instanceVersion string) string {
	for _, v := range []string{engineVersion, instanceVersion} {
		if v == "" {
			continue
		}
		if major, _, ok := strings.Cut(v, "."); ok {
			return major
		}
		return v
	}
	// Provider default engine image when version is omitted.
	major, _, _ := strings.Cut(common.PostgresDefaultVersion, ".")
	return major
}

// ExtensionImage is a catalog extension entry and its CREATE EXTENSION version.
type ExtensionImage struct {
	Config     cnpgv1.ExtensionConfiguration
	SQLVersion string
}

// ExtensionImages holds the resolved images of the enabled extensions by entry name.
type ExtensionImages map[string]ExtensionImage

// ResolveExtensionImages looks up the enabled image-volume extensions in the
// named ClusterImageCatalog, for the instance's PostgreSQL major. The catalog is
// only read when such an extension is enabled.
func ResolveExtensionImages(
	c *controller.Context, catalogName string, custom *components.CNPGCustomSpec, engineVersion, instanceVersion string,
) (ExtensionImages, error) {
	var wanted []extension
	for _, ext := range enabledExtensions(custom) {
		if ext.imageName != "" {
			wanted = append(wanted, ext)
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	major := postgresMajor(engineVersion, instanceVersion)
	majorNumber, err := strconv.Atoi(major)
	if err != nil {
		return nil, fmt.Errorf("invalid PostgreSQL major %q: %w", major, err)
	}

	catalog := &cnpgv1.ClusterImageCatalog{}
	if err := c.Client().Get(c.Context(), client.ObjectKey{Name: catalogName}, catalog); err != nil {
		return nil, fmt.Errorf("get the extensions ClusterImageCatalog %q: %w", catalogName, err)
	}

	entries, _ := catalog.Spec.FindExtensionsForMajor(majorNumber)
	images := ExtensionImages{}
	for _, ext := range wanted {
		i := slices.IndexFunc(entries, func(e cnpgv1.ExtensionConfiguration) bool { return e.Name == ext.imageName })
		if i < 0 {
			return nil, fmt.Errorf("extensions.%s: ClusterImageCatalog %q has no %q extension for PostgreSQL %d",
				ext.field, catalogName, ext.imageName, majorNumber)
		}
		images[ext.imageName] = ExtensionImage{
			Config:     entries[i],
			SQLVersion: catalog.Annotations[SQLVersionAnnotationPrefix+major+"."+ext.imageName],
		}
	}
	return images, nil
}

// ApplyExtensions mounts the extension images of the enabled extensions and
// sets their shared_preload_libraries, default parameters and bootstrap SQL on
// the Cluster. Entries the user already set in parameters.postgresql win.
func ApplyExtensions(pg *cnpgv1.Cluster, custom *components.CNPGCustomSpec, images ExtensionImages) {
	cfg := &pg.Spec.PostgresConfiguration
	for _, ext := range enabledExtensions(custom) {
		if image, ok := images[ext.imageName]; ok && !slices.ContainsFunc(cfg.Extensions, func(e cnpgv1.ExtensionConfiguration) bool { return e.Name == ext.imageName }) {
			cfg.Extensions = append(cfg.Extensions, *image.Config.DeepCopy())
		}

		for _, library := range ext.preloadLibraries {
			if !slices.Contains(cfg.AdditionalLibraries, library) {
				cfg.AdditionalLibraries = append(cfg.AdditionalLibraries, library)
			}
		}

		if len(ext.defaultParameters) > 0 && cfg.Parameters == nil {
			cfg.Parameters = map[string]string{}
		}
		for name, value := range ext.defaultParameters {
			if _, set := cfg.Parameters[name]; !set {
				cfg.Parameters[name] = value
			}
		}

		if ext.bootstrapSQL != nil {
			_, owner := applicationDatabaseIdentity(custom)
			statement := ext.bootstrapSQL(owner)
			if pg.Spec.Bootstrap == nil {
				pg.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{}
			}
			// Copy: InitDB may point into the decoded Instance parameters.
			initDB := &cnpgv1.BootstrapInitDB{}
			if pg.Spec.Bootstrap.InitDB != nil {
				initDB = pg.Spec.Bootstrap.InitDB.DeepCopy()
			}
			if !slices.Contains(initDB.PostInitApplicationSQL, statement) {
				initDB.PostInitApplicationSQL = append(initDB.PostInitApplicationSQL, statement)
			}
			pg.Spec.Bootstrap.InitDB = initDB
		}
	}
}

// ValidateExtensionsAtCreation rejects enabling an extension that needs
// bootstrap SQL on a Cluster that was created without it.
func ValidateExtensionsAtCreation(c *controller.Context, custom *components.CNPGCustomSpec) error {
	pg := &cnpgv1.Cluster{}
	if err := c.Get(pg, c.Name()); err != nil {
		if controller.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Cluster: %w", err)
	}

	for _, ext := range enabledExtensions(custom) {
		if ext.bootstrapSQL == nil {
			continue
		}
		if !slices.ContainsFunc(pg.Spec.PostgresConfiguration.Extensions, func(e cnpgv1.ExtensionConfiguration) bool { return e.Name == ext.imageName }) {
			return fmt.Errorf("extensions.%s can only be enabled when the instance is created", ext.field)
		}
	}
	return nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ExtensionsDatabaseName is the owned Database object that installs every
// enabled extension. CNPG allows a single Database object per database.
func ExtensionsDatabaseName(instanceName string) string {
	return instanceName + "-extensions"
}

func createdExtensions(custom *components.CNPGCustomSpec) []extension {
	var created []extension
	for _, ext := range enabledExtensions(custom) {
		if ext.sqlName != "" {
			created = append(created, ext)
		}
	}
	return created
}

// BuildExtensionsDatabase builds the CNPG Database CR that runs CREATE EXTENSION
// for the enabled extensions in the application database, or nil if none need it.
func BuildExtensionsDatabase(clusterName string, custom *components.CNPGCustomSpec, images ExtensionImages) *cnpgv1.Database {
	created := createdExtensions(custom)
	if len(created) == 0 {
		return nil
	}

	specs := make([]cnpgv1.ExtensionSpec, 0, len(created))
	for _, ext := range created {
		specs = append(specs, cnpgv1.ExtensionSpec{
			DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{
				Name:   ext.sqlName,
				Ensure: cnpgv1.EnsurePresent,
			},
			// Empty installs the default version of the extension's control file.
			Version: images[ext.imageName].SQLVersion,
		})
	}

	dbName, owner := applicationDatabaseIdentity(custom)
	return &cnpgv1.Database{
		Spec: cnpgv1.DatabaseSpec{
			Name:  dbName,
			Owner: owner,
			ClusterRef: corev1.LocalObjectReference{
				Name: clusterName,
			},
			// retain: deleting this CR must not DROP the application database.
			ReclaimPolicy: cnpgv1.DatabaseReclaimRetain,
			Extensions:    specs,
		},
	}
}

// SyncExtensionsDatabase applies the owned extensions Database CR. Disabling an
// installed extension is rejected by Validate, so nothing is removed here.
func SyncExtensionsDatabase(c *controller.Context, custom *components.CNPGCustomSpec, images ExtensionImages) error {
	db := BuildExtensionsDatabase(c.Name(), custom, images)
	if db == nil {
		return nil
	}

	db.ObjectMeta = c.ObjectMeta(ExtensionsDatabaseName(c.Name()))
	if err := c.Apply(db); err != nil {
		return fmt.Errorf("apply extensions Database: %w", err)
	}
	return nil
}

// ValidateExtensionsNotDisabled rejects turning off an extension the provider
// already created. Dropping it would fail or destroy data (hypertables, vector
// and geometry columns, indexes depend on it).
func ValidateExtensionsNotDisabled(c *controller.Context, custom *components.CNPGCustomSpec) error {
	db := &cnpgv1.Database{}
	if err := c.Get(db, ExtensionsDatabaseName(c.Name())); err != nil {
		if controller.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get extensions Database: %w", err)
	}

	for _, ext := range firstClassExtensions {
		if ext.sqlName == "" || ext.enabled(custom) {
			continue
		}
		if slices.ContainsFunc(db.Spec.Extensions, func(e cnpgv1.ExtensionSpec) bool { return e.Name == ext.sqlName }) {
			return fmt.Errorf("extensions.%s cannot be disabled once enabled", ext.field)
		}
	}
	return nil
}

// ExtensionsStatus gates Instance readiness on the extensions Database CR.
// Returns blocked=true while an enabled extension is still being created.
func ExtensionsStatus(c *controller.Context, custom *components.CNPGCustomSpec) (controller.Status, bool) {
	created := createdExtensions(custom)
	if len(created) == 0 {
		return controller.Status{}, false
	}

	db := &cnpgv1.Database{}
	if err := c.Get(db, ExtensionsDatabaseName(c.Name())); err != nil {
		if controller.IsNotFound(err) {
			return controller.Provisioning("waiting for the extensions Database resource"), true
		}
		return controller.Provisioning(fmt.Sprintf("waiting to get the extensions Database: %v", err)), true
	}

	if db.Status.ObservedGeneration != db.Generation {
		return controller.Provisioning("waiting for the extensions Database to be reconciled"), true
	}

	if db.Status.Applied == nil || !*db.Status.Applied {
		msg := db.Status.Message
		if msg == "" {
			msg = "waiting for the extensions Database to be applied"
		}
		return controller.Provisioning(msg), true
	}

	for _, ext := range created {
		for _, status := range db.Status.Extensions {
			if status.Name == ext.sqlName && !status.Applied {
				msg := status.Message
				if msg == "" {
					msg = fmt.Sprintf("waiting for extension %s to be created", ext.sqlName)
				}
				return controller.Provisioning(msg), true
			}
		}
	}

	return controller.Status{}, false
}

func applicationDatabaseIdentity(custom *components.CNPGCustomSpec) (name, owner string) {
	name = cnpgv1.DefaultApplicationDatabaseName
	owner = cnpgv1.DefaultApplicationUserName
	if custom != nil && custom.Bootstrap != nil && custom.Bootstrap.InitDB != nil {
		if custom.Bootstrap.InitDB.Database != "" {
			name = custom.Bootstrap.InitDB.Database
		}
		if custom.Bootstrap.InitDB.Owner != "" {
			owner = custom.Bootstrap.InitDB.Owner
		}
	}
	return name, owner
}
