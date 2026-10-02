package cnpg

import (
	"context"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

var timescaleEnabled = &components.CNPGCustomSpec{
	Extensions: &components.ExtensionsSpec{
		TimescaleDB: &components.TimescaleDBSpec{Enabled: true},
	},
}

func newTimescaleTestContext(t *testing.T, objects ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")
}

func timescaleDatabase(generation, observedGeneration int64, applied bool) *cnpgv1.Database {
	return &cnpgv1.Database{
		ObjectMeta: metav1.ObjectMeta{
			Name:       TimescaleDBDatabaseName("pg"),
			Namespace:  "ns",
			Generation: generation,
		},
		Status: cnpgv1.DatabaseStatus{
			ObservedGeneration: observedGeneration,
			Applied:            pointer.To(applied),
			Extensions:         []cnpgv1.DatabaseObjectStatus{{Name: timescaleSQLName, Applied: applied}},
		},
	}
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

func TestValidateTimescaleDBNotDisabled(t *testing.T) {
	tests := []struct {
		name       string
		custom     *components.CNPGCustomSpec
		dbExists   bool
		wantReject bool
	}{
		{name: "never enabled", custom: nil},
		{name: "enabled with existing Database", custom: timescaleEnabled, dbExists: true},
		{name: "disabled after being enabled", custom: nil, dbExists: true, wantReject: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.dbExists {
				objects = append(objects, timescaleDatabase(1, 1, true))
			}
			err := ValidateTimescaleDBNotDisabled(newTimescaleTestContext(t, objects...), tt.custom)
			if gotReject := err != nil; gotReject != tt.wantReject {
				t.Fatalf("rejected = %t, want %t (err: %v)", gotReject, tt.wantReject, err)
			}
		})
	}
}

func TestTimescaleDBStatus(t *testing.T) {
	tests := []struct {
		name        string
		db          *cnpgv1.Database
		wantBlocked bool
	}{
		{name: "Database missing", wantBlocked: true},
		{name: "status from a previous generation", db: timescaleDatabase(2, 1, true), wantBlocked: true},
		{name: "extension not applied", db: timescaleDatabase(1, 1, false), wantBlocked: true},
		{name: "extension applied", db: timescaleDatabase(1, 1, true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.db != nil {
				objects = append(objects, tt.db)
			}
			status, blocked := TimescaleDBStatus(newTimescaleTestContext(t, objects...), timescaleEnabled)
			if blocked != tt.wantBlocked {
				t.Fatalf("blocked = %t, want %t (status: %+v)", blocked, tt.wantBlocked, status)
			}
		})
	}
}
