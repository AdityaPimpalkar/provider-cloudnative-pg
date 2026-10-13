package cnpg

import (
	"context"
	"slices"
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

var on = &components.ExtensionToggle{Enabled: true}

func withExtensions(extensions components.ExtensionsSpec) *components.CNPGCustomSpec {
	return &components.CNPGCustomSpec{Extensions: &extensions}
}

func newExtensionsTestContext(t *testing.T, objects ...client.Object) (*controller.Context, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cnpgv1.AddToScheme, corev1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns", UID: "uid"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg"), cl
}

// extensionsDatabase is the Database CR as left by a previous Sync, with the
// given extensions created (or still pending when applied is false).
func extensionsDatabase(generation, observedGeneration int64, applied bool, sqlNames ...string) *cnpgv1.Database {
	db := &cnpgv1.Database{
		ObjectMeta: metav1.ObjectMeta{
			Name:       ExtensionsDatabaseName("pg"),
			Namespace:  "ns",
			Generation: generation,
		},
		Status: cnpgv1.DatabaseStatus{
			ObservedGeneration: observedGeneration,
			Applied:            pointer.To(applied),
		},
	}
	for _, name := range sqlNames {
		db.Spec.Extensions = append(db.Spec.Extensions, cnpgv1.ExtensionSpec{
			DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsurePresent},
		})
		db.Status.Extensions = append(db.Status.Extensions, cnpgv1.DatabaseObjectStatus{Name: name, Applied: applied})
	}
	return db
}

func TestValidateExtensions(t *testing.T) {
	tests := []struct {
		name            string
		custom          *components.CNPGCustomSpec
		engineVersion   string
		instanceVersion string
		wantErr         bool
	}{
		{name: "none enabled on PG 17", engineVersion: "17.10"},
		{name: "image volume on PG 18", custom: withExtensions(components.ExtensionsSpec{TimescaleDB: on}), engineVersion: "18.4"},
		{name: "image volume on PG 18 from spec.version", custom: withExtensions(components.ExtensionsSpec{PostGIS: on}), instanceVersion: "18.4"},
		{name: "image volume on PG 17", custom: withExtensions(components.ExtensionsSpec{PgIVM: on}), engineVersion: "17.10", wantErr: true},
		{name: "image volume on the default version", custom: withExtensions(components.ExtensionsSpec{Wal2JSON: on}), wantErr: true},
		{name: "bundled extensions on PG 14", custom: withExtensions(components.ExtensionsSpec{PgVector: on, PgAudit: on}), engineVersion: "14.23"},
		{name: "pgrouting with postgis", custom: withExtensions(components.ExtensionsSpec{PostGIS: on, PgRouting: on}), engineVersion: "18.4"},
		{name: "pgrouting without postgis", custom: withExtensions(components.ExtensionsSpec{PgRouting: on}), engineVersion: "18.4", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExtensions(tt.custom, tt.engineVersion, tt.instanceVersion)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateExtensions() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestApplyExtensions(t *testing.T) {
	pg := &cnpgv1.Cluster{}
	pg.Spec.PostgresConfiguration.Parameters = map[string]string{"max_locks_per_transaction": "512"}
	pg.Spec.PostgresConfiguration.AdditionalLibraries = []string{"pg_stat_statements"}

	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{
		TimescaleDB: on, PostGIS: on, PgIVM: on, PgVector: on, PgAudit: on,
	}))

	cfg := pg.Spec.PostgresConfiguration
	var images []string
	for _, e := range cfg.Extensions {
		images = append(images, e.Name)
	}
	if want := []string{"timescaledb-oss", "postgis", "pg-ivm"}; !slices.Equal(images, want) {
		t.Errorf("extension images = %v, want %v", images, want)
	}
	postgis := cfg.Extensions[1]
	if len(postgis.LdLibraryPath) != 1 || postgis.LdLibraryPath[0] != "system" || len(postgis.Env) != 2 {
		t.Errorf("postgis ld_library_path/env = %v/%v", postgis.LdLibraryPath, postgis.Env)
	}
	if want := []string{"pg_stat_statements", "timescaledb", "pg_ivm"}; !slices.Equal(cfg.AdditionalLibraries, want) {
		t.Errorf("shared_preload_libraries = %v, want %v", cfg.AdditionalLibraries, want)
	}
	for name, want := range map[string]string{
		"max_locks_per_transaction":   "512",
		"timescaledb.telemetry_level": "off",
		"pgaudit.log":                 "ddl, role",
	} {
		if got := cfg.Parameters[name]; got != want {
			t.Errorf("parameter %s = %q, want %q", name, got, want)
		}
	}

	untouched := &cnpgv1.Cluster{}
	ApplyExtensions(untouched, nil)
	if cfg := untouched.Spec.PostgresConfiguration; cfg.Extensions != nil || cfg.AdditionalLibraries != nil || cfg.Parameters != nil {
		t.Errorf("expected no changes without extensions, got %+v", cfg)
	}
}

func TestBuildExtensionsDatabase(t *testing.T) {
	if db := BuildExtensionsDatabase("my-pg", withExtensions(components.ExtensionsSpec{PgAudit: on, Wal2JSON: on})); db != nil {
		t.Fatalf("expected no Database for extensions without CREATE EXTENSION, got %+v", db)
	}

	db := BuildExtensionsDatabase("my-pg", withExtensions(components.ExtensionsSpec{
		PgRouting: on, PostGIS: on, PgVector: on, PgAudit: on,
	}))
	if db.Spec.Name != "app" || db.Spec.Owner != "app" || db.Spec.ClusterRef.Name != "my-pg" {
		t.Fatalf("unexpected identity: name=%q owner=%q cluster=%q", db.Spec.Name, db.Spec.Owner, db.Spec.ClusterRef.Name)
	}
	if db.Spec.ReclaimPolicy != cnpgv1.DatabaseReclaimRetain {
		t.Fatalf("expected reclaim retain, got %q", db.Spec.ReclaimPolicy)
	}
	want := []cnpgv1.ExtensionSpec{
		{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: "postgis", Ensure: cnpgv1.EnsurePresent}, Version: "3.6.4"},
		{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: "pgrouting", Ensure: cnpgv1.EnsurePresent}, Version: "4.0.1"},
		{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: "vector", Ensure: cnpgv1.EnsurePresent}},
	}
	if len(db.Spec.Extensions) != len(want) {
		t.Fatalf("extensions = %+v, want %+v", db.Spec.Extensions, want)
	}
	for i := range want {
		if db.Spec.Extensions[i] != want[i] {
			t.Errorf("extension %d = %+v, want %+v", i, db.Spec.Extensions[i], want[i])
		}
	}

	custom := withExtensions(components.ExtensionsSpec{PgVector: on})
	custom.Bootstrap = &components.BootstrapConfiguration{
		InitDB: &cnpgv1.BootstrapInitDB{Database: "embeddings", Owner: "ml"},
	}
	db = BuildExtensionsDatabase("my-pg", custom)
	if db.Spec.Name != "embeddings" || db.Spec.Owner != "ml" {
		t.Fatalf("unexpected identity: name=%q owner=%q", db.Spec.Name, db.Spec.Owner)
	}
}

func TestSyncExtensionsDatabase(t *testing.T) {
	c, cl := newExtensionsTestContext(t)
	if err := SyncExtensionsDatabase(c, withExtensions(components.ExtensionsSpec{PgVector: on})); err != nil {
		t.Fatalf("SyncExtensionsDatabase: %v", err)
	}

	db := &cnpgv1.Database{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "pg-extensions"}, db); err != nil {
		t.Fatalf("get Database: %v", err)
	}
	if len(db.Spec.Extensions) != 1 || db.Spec.Extensions[0].Name != "vector" {
		t.Errorf("extensions = %+v, want vector", db.Spec.Extensions)
	}
	if len(db.OwnerReferences) != 1 || db.OwnerReferences[0].Name != "pg" {
		t.Errorf("ownerReferences = %+v, want the Instance", db.OwnerReferences)
	}

	c, cl = newExtensionsTestContext(t)
	if err := SyncExtensionsDatabase(c, nil); err != nil {
		t.Fatalf("SyncExtensionsDatabase without extensions: %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "pg-extensions"}, db); err == nil {
		t.Error("expected no Database without extensions")
	}
}

func TestValidateExtensionsNotDisabled(t *testing.T) {
	tests := []struct {
		name       string
		custom     *components.CNPGCustomSpec
		created    []string
		wantReject bool
	}{
		{name: "never enabled"},
		{name: "enabling on a fresh instance", custom: withExtensions(components.ExtensionsSpec{PgVector: on})},
		{name: "enabling one more", custom: withExtensions(components.ExtensionsSpec{TimescaleDB: on, PgVector: on}), created: []string{"timescaledb"}},
		{name: "kept", custom: withExtensions(components.ExtensionsSpec{PgVector: on}), created: []string{"vector"}},
		{name: "pgvector disabled", created: []string{"vector"}, wantReject: true},
		{name: "timescaledb disabled, pgvector kept", custom: withExtensions(components.ExtensionsSpec{PgVector: on}), created: []string{"timescaledb", "vector"}, wantReject: true},
		{name: "operator-managed pgaudit disabled", custom: withExtensions(components.ExtensionsSpec{PgVector: on}), created: []string{"vector"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.created != nil {
				objects = append(objects, extensionsDatabase(1, 1, true, tt.created...))
			}
			c, _ := newExtensionsTestContext(t, objects...)
			err := ValidateExtensionsNotDisabled(c, tt.custom)
			if gotReject := err != nil; gotReject != tt.wantReject {
				t.Fatalf("rejected = %t, want %t (err: %v)", gotReject, tt.wantReject, err)
			}
		})
	}
}

func TestExtensionsStatus(t *testing.T) {
	pgvector := withExtensions(components.ExtensionsSpec{PgVector: on})
	tests := []struct {
		name        string
		custom      *components.CNPGCustomSpec
		db          *cnpgv1.Database
		wantBlocked bool
	}{
		{name: "no extensions enabled"},
		{name: "only extensions without CREATE EXTENSION", custom: withExtensions(components.ExtensionsSpec{PgAudit: on})},
		{name: "Database missing", custom: pgvector, wantBlocked: true},
		{name: "status from a previous generation", custom: pgvector, db: extensionsDatabase(2, 1, true, "vector"), wantBlocked: true},
		{name: "extension not applied", custom: pgvector, db: extensionsDatabase(1, 1, false, "vector"), wantBlocked: true},
		{name: "extension applied", custom: pgvector, db: extensionsDatabase(1, 1, true, "vector")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.db != nil {
				objects = append(objects, tt.db)
			}
			c, _ := newExtensionsTestContext(t, objects...)
			status, blocked := ExtensionsStatus(c, tt.custom)
			if blocked != tt.wantBlocked {
				t.Fatalf("blocked = %t, want %t (status: %+v)", blocked, tt.wantBlocked, status)
			}
		})
	}
}

