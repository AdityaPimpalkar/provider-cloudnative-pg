package cnpg

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// TimescaleDB image/volume names and SQL extension version come from the
// official timescaledb-oss metadata (PG 18 / trixie only as of that catalog):
// https://github.com/cloudnative-pg/postgres-extensions-containers/blob/main/timescaledb-oss/metadata.hcl
const (
	timescaleExtensionName = "timescaledb-oss"
	timescaleLibraryName   = "timescaledb"
	timescaleSQLName       = "timescaledb"
	timescaleSQLVersion    = "2.30.1"
	timescaleImageRef      = "ghcr.io/cloudnative-pg/timescaledb-oss:2.30.1-18-trixie"

	timescalePostgresSupportVersion = "18"
)

// Defaults from the upstream timescaledb-oss Cluster example; user-set values win.
var timescaleDefaultParameters = map[string]string{
	"timescaledb.telemetry_level": "off",
	"max_locks_per_transaction":   "128",
}

// IsTimescaleDBEnabled reports whether the first-class TimescaleDB toggle is on.
func IsTimescaleDBEnabled(custom *components.CNPGCustomSpec) bool {
	return custom != nil &&
		custom.Extensions != nil &&
		custom.Extensions.TimescaleDB != nil &&
		custom.Extensions.TimescaleDB.Enabled
}

// ValidateTimescaleDB ensures the engine major version is compatible with the
// published timescaledb-oss image (PostgreSQL 18 only). ImageVolume support
// cannot be probed from Validate; callers must run on Kubernetes 1.35+ (or
// 1.33/1.34 with the ImageVolume feature gate).
func ValidateTimescaleDB(custom *components.CNPGCustomSpec, engineVersion, instanceVersion string) error {
	if !IsTimescaleDBEnabled(custom) {
		return nil
	}

	major := postgresMajor(engineVersion, instanceVersion)
	if major != timescalePostgresSupportVersion {
		return fmt.Errorf(
			"extensions.timescaledb requires PostgreSQL 18 (got major %q); set spec.version to a 18.x release (e.g. \"18.4\"); also requires Kubernetes ImageVolume (1.35+, or 1.33/1.34 with the ImageVolume feature gate)",
			major,
		)
	}
	return nil
}

// ValidateTimescaleDBNotDisabled rejects turning TimescaleDB off once its Database
// CR exists. Dropping the extension safely needs the library loaded and no
// dependent hypertables, which the provider cannot guarantee.
func ValidateTimescaleDBNotDisabled(c *controller.Context, custom *components.CNPGCustomSpec) error {
	if IsTimescaleDBEnabled(custom) {
		return nil
	}

	err := c.Get(&cnpgv1.Database{}, TimescaleDBDatabaseName(c.Name()))
	if err == nil {
		return errors.New("extensions.timescaledb cannot be disabled once enabled")
	}
	if controller.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("get timescaledb Database: %w", err)
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

// BuildTimescaleDBExtension configures the Cluster for TimescaleDB install:
// extension image volume, shared_preload_libraries and default parameters.
// Matches the upstream timescaledb-oss README Cluster example.
// https://github.com/cloudnative-pg/postgres-extensions-containers/tree/main/timescaledb-oss#1-add-the-timescaledb-extension-image-to-your-cluster
func BuildTimescaleDBExtension(pg *cnpgv1.Cluster) {
	cfg := &pg.Spec.PostgresConfiguration

	if !hasExtension(cfg.Extensions, timescaleExtensionName) {
		cfg.Extensions = append(cfg.Extensions, cnpgv1.ExtensionConfiguration{
			Name: timescaleExtensionName,
			ImageVolumeSource: corev1.ImageVolumeSource{
				Reference: timescaleImageRef,
			},
		})
	}

	if !slices.Contains(cfg.AdditionalLibraries, timescaleLibraryName) {
		cfg.AdditionalLibraries = append(cfg.AdditionalLibraries, timescaleLibraryName)
	}

	if cfg.Parameters == nil {
		cfg.Parameters = map[string]string{}
	}
	for name, value := range timescaleDefaultParameters {
		if _, set := cfg.Parameters[name]; !set {
			cfg.Parameters[name] = value
		}
	}
}

// BuildTimescaleDBDatabase builds the CNPG Database CR that enables
// CREATE EXTENSION timescaledb in the application database.
func BuildTimescaleDBDatabase(clusterName string, custom *components.CNPGCustomSpec) *cnpgv1.Database {
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
			Extensions: []cnpgv1.ExtensionSpec{{
				DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{
					Name:   timescaleSQLName,
					Ensure: cnpgv1.EnsurePresent,
				},
				Version: timescaleSQLVersion,
			}},
		},
	}
}

// SyncTimescaleDBDatabase applies the owned Database CR that runs
// CREATE EXTENSION timescaledb. Disabling is rejected by Validate.
func SyncTimescaleDBDatabase(c *controller.Context, custom *components.CNPGCustomSpec) error {
	if !IsTimescaleDBEnabled(custom) {
		return nil
	}

	db := BuildTimescaleDBDatabase(c.Name(), custom)
	db.ObjectMeta = c.ObjectMeta(TimescaleDBDatabaseName(c.Name()))
	if err := c.Apply(db); err != nil {
		return fmt.Errorf("apply timescaledb Database: %w", err)
	}
	return nil
}

// TimescaleDBStatus gates Instance readiness on the Database CR when TimescaleDB
// is enabled. Returns blocked=true while the extension is still reconciling.
func TimescaleDBStatus(c *controller.Context, custom *components.CNPGCustomSpec) (controller.Status, bool) {
	if !IsTimescaleDBEnabled(custom) {
		return controller.Status{}, false
	}

	db := &cnpgv1.Database{}
	if err := c.Get(db, TimescaleDBDatabaseName(c.Name())); err != nil {
		if controller.IsNotFound(err) {
			return controller.Provisioning("waiting for TimescaleDB Database resource"), true
		}
		return controller.Provisioning(fmt.Sprintf("waiting to get TimescaleDB Database: %v", err)), true
	}

	if db.Status.ObservedGeneration != db.Generation {
		return controller.Provisioning("waiting for TimescaleDB Database to be reconciled"), true
	}

	if db.Status.Applied == nil || !*db.Status.Applied {
		msg := db.Status.Message
		if msg == "" {
			msg = "waiting for TimescaleDB extension to be applied"
		}
		return controller.Provisioning(msg), true
	}

	for _, ext := range db.Status.Extensions {
		if ext.Name == timescaleSQLName && !ext.Applied {
			msg := ext.Message
			if msg == "" {
				msg = "waiting for TimescaleDB extension to be installed"
			}
			return controller.Provisioning(msg), true
		}
	}

	return controller.Status{}, false
}

// TimescaleDBDatabaseName is the owned Database object name for an Instance.
func TimescaleDBDatabaseName(instanceName string) string {
	return instanceName + "-timescaledb"
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

func hasExtension(exts []cnpgv1.ExtensionConfiguration, name string) bool {
	for _, e := range exts {
		if e.Name == name {
			return true
		}
	}
	return false
}
