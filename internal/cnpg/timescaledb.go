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
// extension image volume and shared_preload_libraries.
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
}

// BuildTimescaleDBDatabase builds the CNPG Database CR that enables
// CREATE EXTENSION timescaledb in the application database.
func BuildTimescaleDBDatabase(clusterName string, custom *components.CNPGCustomSpec) *cnpgv1.Database {
	return buildTimescaleDBDatabase(clusterName, custom, cnpgv1.EnsurePresent)
}

func buildTimescaleDBDatabase(clusterName string, custom *components.CNPGCustomSpec, ensure cnpgv1.EnsureOption) *cnpgv1.Database {
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
					Ensure: ensure,
				},
				Version: timescaleSQLVersion,
			}},
		},
	}
}

// SyncTimescaleDBDatabase applies or cleans up the owned Database CR.
// When enabled, ensures CREATE EXTENSION. When disabled after a prior enable,
// flips the extension to EnsureAbsent, waits for CNPG to apply it, then
// deletes the Database CR (reclaim=retain so the app DB is kept).
func SyncTimescaleDBDatabase(c *controller.Context, custom *components.CNPGCustomSpec) error {
	name := TimescaleDBDatabaseName(c.Name())

	if IsTimescaleDBEnabled(custom) {
		db := BuildTimescaleDBDatabase(c.Name(), custom)
		db.ObjectMeta = c.ObjectMeta(name)
		if err := c.Apply(db); err != nil {
			return fmt.Errorf("apply timescaledb Database: %w", err)
		}
		return nil
	}

	existing := &cnpgv1.Database{}
	if err := c.Get(existing, name); err != nil {
		if controller.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get timescaledb Database: %w", err)
	}

	if timescaleExtensionEnsure(existing) != cnpgv1.EnsureAbsent {
		// Preserve Name/Owner and ObjectMeta from the live object — Database.Name
		// is immutable and finalizers must survive the update.
		db := existing.DeepCopy()
		db.Spec.ReclaimPolicy = cnpgv1.DatabaseReclaimRetain
		db.Spec.Extensions = []cnpgv1.ExtensionSpec{{
			DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{
				Name:   timescaleSQLName,
				Ensure: cnpgv1.EnsureAbsent,
			},
			Version: timescaleSQLVersion,
		}}
		if err := c.Apply(db); err != nil {
			return fmt.Errorf("apply timescaledb Database (ensure absent): %w", err)
		}
		return nil
	}

	if !timescaleExtensionAbsentApplied(existing) {
		// Wait for DROP EXTENSION; WatchOwned(Database) will requeue.
		return nil
	}

	if err := c.Delete(existing); err != nil {
		return fmt.Errorf("delete timescaledb Database: %w", err)
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

func timescaleExtensionEnsure(db *cnpgv1.Database) cnpgv1.EnsureOption {
	for _, ext := range db.Spec.Extensions {
		if ext.Name == timescaleSQLName {
			if ext.Ensure == "" {
				return cnpgv1.EnsurePresent
			}
			return ext.Ensure
		}
	}
	return cnpgv1.EnsurePresent
}

func timescaleExtensionAbsentApplied(db *cnpgv1.Database) bool {
	if db.Status.Applied == nil || !*db.Status.Applied {
		return false
	}
	for _, ext := range db.Status.Extensions {
		if ext.Name == timescaleSQLName {
			return ext.Applied
		}
	}
	// Extension no longer reported — treat as successfully removed.
	return true
}
