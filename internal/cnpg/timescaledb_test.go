package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

var timescaleEnabled = &components.CNPGCustomSpec{
	Extensions: &components.ExtensionsSpec{
		TimescaleDB: &components.TimescaleDBSpec{Enabled: true},
	},
}

func TestValidateTimescaleDB(t *testing.T) {
	enabled := timescaleEnabled

	if err := ValidateTimescaleDB(enabled, "18.4", ""); err != nil {
		t.Fatalf("expected PG 18 to be valid: %v", err)
	}
	if err := ValidateTimescaleDB(enabled, "", "18.4"); err != nil {
		t.Fatalf("expected instance version 18.4 to be valid: %v", err)
	}
	if err := ValidateTimescaleDB(enabled, "17.10", ""); err == nil {
		t.Fatal("expected PG 17 to be rejected")
	}
	if err := ValidateTimescaleDB(nil, "17.10", ""); err != nil {
		t.Fatalf("disabled should skip validation: %v", err)
	}
}

func TestBuildTimescaleDBExtension(t *testing.T) {
	pg := &cnpgv1.Cluster{}
	pg.Spec.PostgresConfiguration.Parameters = map[string]string{
		"max_connections": "100",
	}

	BuildTimescaleDBExtension(pg)

	cfg := pg.Spec.PostgresConfiguration
	if len(cfg.Extensions) != 1 || cfg.Extensions[0].Name != timescaleExtensionName {
		t.Fatalf("unexpected extensions: %+v", cfg.Extensions)
	}
	if cfg.Extensions[0].ImageVolumeSource.Reference != timescaleImageRef {
		t.Fatalf("unexpected image: %q", cfg.Extensions[0].ImageVolumeSource.Reference)
	}
	if len(cfg.AdditionalLibraries) != 1 || cfg.AdditionalLibraries[0] != timescaleLibraryName {
		t.Fatalf("unexpected shared_preload_libraries: %+v", cfg.AdditionalLibraries)
	}
	for name, want := range map[string]string{
		"max_connections":             "100",
		"timescaledb.telemetry_level": "off",
		"max_locks_per_transaction":   "128",
	} {
		if got := cfg.Parameters[name]; got != want {
			t.Errorf("parameter %s = %q, want %q", name, got, want)
		}
	}
}

func TestBuildTimescaleDBExtensionKeepsUserParameters(t *testing.T) {
	pg := &cnpgv1.Cluster{}
	pg.Spec.PostgresConfiguration.Parameters = map[string]string{
		"timescaledb.telemetry_level": "basic",
		"max_locks_per_transaction":   "512",
	}

	BuildTimescaleDBExtension(pg)

	params := pg.Spec.PostgresConfiguration.Parameters
	if params["timescaledb.telemetry_level"] != "basic" || params["max_locks_per_transaction"] != "512" {
		t.Fatalf("user parameters overridden: %+v", params)
	}
}

func TestBuildTimescaleDBExtensionNilParameters(t *testing.T) {
	pg := &cnpgv1.Cluster{}

	BuildTimescaleDBExtension(pg)

	if got := pg.Spec.PostgresConfiguration.Parameters["timescaledb.telemetry_level"]; got != "off" {
		t.Fatalf("telemetry_level = %q, want off", got)
	}
}
