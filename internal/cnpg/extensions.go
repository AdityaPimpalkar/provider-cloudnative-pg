package cnpg

import (
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// Image volumes need extension_control_path, added in PostgreSQL 18.
const imageVolumePostgresMajor = "18"

// extension is a first-class toggle under parameters.extensions.
//
// Images are the trixie builds (matching the standard-trixie operand images)
// from the official catalog image-catalogs-extensions/catalog-minimal-trixie.yaml
// in https://github.com/cloudnative-pg/artifacts; SQL names, versions, preload
// libraries, paths and env follow each image's metadata.hcl in
// https://github.com/cloudnative-pg/postgres-extensions-containers.
type extension struct {
	field  string
	toggle func(*components.ExtensionsSpec) *components.ExtensionToggle
	// image is nil when the standard operand images already ship the extension.
	image *cnpgv1.ExtensionConfiguration
	// sqlName is created in the application database; empty when there is no
	// CREATE EXTENSION or the operator manages it.
	sqlName string
	// sqlVersion is empty to install the version shipped in the image.
	sqlVersion       string
	preloadLibraries []string
	// defaultParameters apply unless set in parameters.postgresql.parameters.
	defaultParameters map[string]string
	requires          []string
}

// Ordered so that dependencies are created first (postgis before pgrouting).
var firstClassExtensions = []extension{
	{
		field:  "timescaledb",
		toggle: func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.TimescaleDB },
		image: &cnpgv1.ExtensionConfiguration{
			Name:              "timescaledb-oss",
			ImageVolumeSource: corev1.ImageVolumeSource{Reference: "ghcr.io/cloudnative-pg/timescaledb-oss:2.30.1-18-trixie"},
		},
		sqlName:          "timescaledb",
		sqlVersion:       "2.30.1",
		preloadLibraries: []string{"timescaledb"},
		// From the upstream timescaledb-oss Cluster example.
		defaultParameters: map[string]string{
			"timescaledb.telemetry_level": "off",
			"max_locks_per_transaction":   "128",
		},
	},
	{
		field:  "postgis",
		toggle: func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PostGIS },
		image: &cnpgv1.ExtensionConfiguration{
			Name:              "postgis",
			ImageVolumeSource: corev1.ImageVolumeSource{Reference: "ghcr.io/cloudnative-pg/postgis-extension:3.6.4-202610110422-18-trixie@sha256:84a8e286d79aaec6fe824a792179d11252745a0d2b139d88692424fa61494df0"},
			LdLibraryPath:     []string{"system"},
			Env: []cnpgv1.ExtensionEnvVar{
				{Name: "GDAL_DATA", Value: "${image_root}/share/gdal"},
				{Name: "PROJ_DATA", Value: "${image_root}/share/proj"},
			},
		},
		sqlName:    "postgis",
		sqlVersion: "3.6.4",
	},
	{
		field:  "pgrouting",
		toggle: func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgRouting },
		image: &cnpgv1.ExtensionConfiguration{
			Name:              "pgrouting",
			ImageVolumeSource: corev1.ImageVolumeSource{Reference: "ghcr.io/cloudnative-pg/pgrouting:4.0.1-202610110422-18-trixie@sha256:a9e9f2b786c213bebcb3baffe09d04de844c16bf8420c7bdbd98122c2aa704a1"},
		},
		sqlName:    "pgrouting",
		sqlVersion: "4.0.1",
		requires:   []string{"postgis"},
	},
	{
		field:   "pgvector",
		toggle:  func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgVector },
		sqlName: "vector",
	},
	{
		field:  "pgivm",
		toggle: func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.PgIVM },
		image: &cnpgv1.ExtensionConfiguration{
			Name:              "pg-ivm",
			ImageVolumeSource: corev1.ImageVolumeSource{Reference: "ghcr.io/cloudnative-pg/pg-ivm:1.15-202610110422-18-trixie@sha256:3f6d327ec3f8cdc4212789f0cfc95be81b723e51f869639d526de6236ecae874"},
		},
		sqlName:          "pg_ivm",
		sqlVersion:       "1.15",
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
		field:  "wal2json",
		toggle: func(e *components.ExtensionsSpec) *components.ExtensionToggle { return e.Wal2JSON },
		image: &cnpgv1.ExtensionConfiguration{
			Name:              "wal2json",
			ImageVolumeSource: corev1.ImageVolumeSource{Reference: "ghcr.io/cloudnative-pg/wal2json:2.6-202610110422-18-trixie@sha256:298e20b7e9a75553a7b0070237ff09af9122d5cae2689255c8203283aaf430d0"},
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
		if ext.image != nil && major != imageVolumePostgresMajor {
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

// ApplyExtensions mounts the extension images of the enabled extensions and
// sets their shared_preload_libraries and default parameters on the Cluster.
// Entries the user already set in parameters.postgresql win.
func ApplyExtensions(pg *cnpgv1.Cluster, custom *components.CNPGCustomSpec) {
	cfg := &pg.Spec.PostgresConfiguration
	for _, ext := range enabledExtensions(custom) {
		if ext.image != nil && !slices.ContainsFunc(cfg.Extensions, func(e cnpgv1.ExtensionConfiguration) bool { return e.Name == ext.image.Name }) {
			cfg.Extensions = append(cfg.Extensions, *ext.image.DeepCopy())
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
	}
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
func BuildExtensionsDatabase(clusterName string, custom *components.CNPGCustomSpec) *cnpgv1.Database {
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
			Version: ext.sqlVersion,
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
func SyncExtensionsDatabase(c *controller.Context, custom *components.CNPGCustomSpec) error {
	db := BuildExtensionsDatabase(c.Name(), custom)
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
