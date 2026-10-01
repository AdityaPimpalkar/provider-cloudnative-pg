package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestValidateTimescaleDB(t *testing.T) {
	enabled := &components.CNPGCustomSpec{
		Extensions: &components.ExtensionsSpec{
			TimescaleDB: &components.TimescaleDBSpec{Enabled: true},
		},
	}

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
}

func TestBuildTimescaleDBDatabase(t *testing.T) {
	db := BuildTimescaleDBDatabase("my-pg", nil)
	if db.Spec.Name != "app" || db.Spec.Owner != "app" {
		t.Fatalf("unexpected defaults: name=%q owner=%q", db.Spec.Name, db.Spec.Owner)
	}
	if db.Spec.ClusterRef.Name != "my-pg" {
		t.Fatalf("unexpected cluster ref: %q", db.Spec.ClusterRef.Name)
	}
	if db.Spec.ReclaimPolicy != cnpgv1.DatabaseReclaimRetain {
		t.Fatalf("expected reclaim retain, got %q", db.Spec.ReclaimPolicy)
	}
	if len(db.Spec.Extensions) != 1 ||
		db.Spec.Extensions[0].Name != timescaleSQLName ||
		db.Spec.Extensions[0].Ensure != cnpgv1.EnsurePresent ||
		db.Spec.Extensions[0].Version != timescaleSQLVersion {
		t.Fatalf("unexpected extensions: %+v", db.Spec.Extensions)
	}

	custom := &components.CNPGCustomSpec{
		Bootstrap: &components.BootstrapConfiguration{
			InitDB: &cnpgv1.BootstrapInitDB{Database: "metrics", Owner: "metrics"},
		},
	}
	db = BuildTimescaleDBDatabase("my-pg", custom)
	if db.Spec.Name != "metrics" || db.Spec.Owner != "metrics" {
		t.Fatalf("unexpected identity: name=%q owner=%q", db.Spec.Name, db.Spec.Owner)
	}
}

func TestTimescaleExtensionEnsureHelpers(t *testing.T) {
	present := buildTimescaleDBDatabase("pg", nil, cnpgv1.EnsurePresent)
	if got := timescaleExtensionEnsure(present); got != cnpgv1.EnsurePresent {
		t.Fatalf("expected present, got %q", got)
	}

	absent := buildTimescaleDBDatabase("pg", nil, cnpgv1.EnsureAbsent)
	if got := timescaleExtensionEnsure(absent); got != cnpgv1.EnsureAbsent {
		t.Fatalf("expected absent, got %q", got)
	}

	applied := true
	absent.Status.Applied = &applied
	absent.Status.Extensions = []cnpgv1.DatabaseObjectStatus{{
		Name:    timescaleSQLName,
		Applied: true,
	}}
	if !timescaleExtensionAbsentApplied(absent) {
		t.Fatal("expected absent+applied to be ready for delete")
	}

	absent.Status.Applied = nil
	if timescaleExtensionAbsentApplied(absent) {
		t.Fatal("expected not ready when Applied is unset")
	}
}
