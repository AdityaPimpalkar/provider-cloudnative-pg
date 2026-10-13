package cnpg

import (
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
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

func hasExtension(exts []cnpgv1.ExtensionConfiguration, name string) bool {
	for _, e := range exts {
		if e.Name == name {
			return true
		}
	}
	return false
}
