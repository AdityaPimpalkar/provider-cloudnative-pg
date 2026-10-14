package cnpg

import (
	"cmp"
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
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

	postgis := cnpgv1.ExtensionConfiguration{Name: "postgis", LdLibraryPath: []string{"system"}}
	images := ExtensionImages{
		"timescaledb-oss": {Config: cnpgv1.ExtensionConfiguration{Name: "timescaledb-oss"}},
		"postgis":         {Config: postgis},
		"pg-ivm":          {Config: cnpgv1.ExtensionConfiguration{Name: "pg-ivm"}},
	}
	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{
		TimescaleDB: on, PostGIS: on, PgIVM: on, PgVector: on, PgAudit: on,
	}), images)

	cfg := pg.Spec.PostgresConfiguration
	var names []string
	for _, e := range cfg.Extensions {
		names = append(names, e.Name)
	}
	if want := []string{"timescaledb-oss", "postgis", "pg-ivm"}; !slices.Equal(names, want) {
		t.Errorf("extension images = %v, want %v", names, want)
	}
	if !slices.Equal(cfg.Extensions[1].LdLibraryPath, postgis.LdLibraryPath) {
		t.Errorf("postgis ld_library_path = %v, want the catalog entry", cfg.Extensions[1].LdLibraryPath)
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
	ApplyExtensions(untouched, nil, nil)
	if cfg := untouched.Spec.PostgresConfiguration; cfg.Extensions != nil || cfg.AdditionalLibraries != nil || cfg.Parameters != nil {
		t.Errorf("expected no changes without extensions, got %+v", cfg)
	}
	if untouched.Spec.Bootstrap != nil {
		t.Errorf("expected no bootstrap without extensions, got %+v", untouched.Spec.Bootstrap)
	}
}

func TestApplyExtensionsBootstrapSQL(t *testing.T) {
	ages := ExtensionImages{"age": {Config: cnpgv1.ExtensionConfiguration{Name: "age"}}}
	pg := &cnpgv1.Cluster{}
	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{AGE: on}), ages)
	if pg.Spec.Bootstrap == nil || pg.Spec.Bootstrap.InitDB == nil {
		t.Fatalf("expected initdb bootstrap, got %+v", pg.Spec.Bootstrap)
	}
	if want := []string{`ALTER DEFAULT PRIVILEGES FOR ROLE postgres GRANT USAGE ON SCHEMAS TO "app"`}; !slices.Equal(pg.Spec.Bootstrap.InitDB.PostInitApplicationSQL, want) {
		t.Errorf("postInitApplicationSQL = %q, want %q", pg.Spec.Bootstrap.InitDB.PostInitApplicationSQL, want)
	}

	custom := withExtensions(components.ExtensionsSpec{AGE: on})
	userInitDB := &cnpgv1.BootstrapInitDB{Database: "graphs", Owner: "graph\"er", PostInitApplicationSQL: []string{"SELECT 1"}}
	custom.Bootstrap = &components.BootstrapConfiguration{InitDB: userInitDB}
	pg = &cnpgv1.Cluster{}
	pg.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: userInitDB}
	ApplyExtensions(pg, custom, ages)
	ApplyExtensions(pg, custom, ages)
	want := []string{"SELECT 1", `ALTER DEFAULT PRIVILEGES FOR ROLE postgres GRANT USAGE ON SCHEMAS TO "graph""er"`}
	if got := pg.Spec.Bootstrap.InitDB.PostInitApplicationSQL; !slices.Equal(got, want) {
		t.Errorf("postInitApplicationSQL = %q, want %q", got, want)
	}
	if len(userInitDB.PostInitApplicationSQL) != 1 {
		t.Errorf("user initdb mutated: %q", userInitDB.PostInitApplicationSQL)
	}
}

func TestValidateExtensionsAtCreation(t *testing.T) {
	age := withExtensions(components.ExtensionsSpec{AGE: on})
	clusterWith := func(images ...string) *cnpgv1.Cluster {
		pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
		for _, image := range images {
			pg.Spec.PostgresConfiguration.Extensions = append(pg.Spec.PostgresConfiguration.Extensions, cnpgv1.ExtensionConfiguration{Name: image})
		}
		return pg
	}
	tests := []struct {
		name       string
		custom     *components.CNPGCustomSpec
		cluster    *cnpgv1.Cluster
		wantReject bool
	}{
		{name: "new instance", custom: age},
		{name: "created with age", custom: age, cluster: clusterWith("age")},
		{name: "enabled on an existing instance", custom: age, cluster: clusterWith("pg-ivm"), wantReject: true},
		{name: "other extensions on an existing instance", custom: withExtensions(components.ExtensionsSpec{PgIVM: on}), cluster: clusterWith()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.cluster != nil {
				objects = append(objects, tt.cluster)
			}
			c, _ := newExtensionsTestContext(t, objects...)
			err := ValidateExtensionsAtCreation(c, tt.custom)
			if gotReject := err != nil; gotReject != tt.wantReject {
				t.Fatalf("rejected = %t, want %t (err: %v)", gotReject, tt.wantReject, err)
			}
		})
	}
}

func TestResolveExtensionImages(t *testing.T) {
	catalog := &cnpgv1.ClusterImageCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "extensions",
			Annotations: map[string]string{SQLVersionAnnotationPrefix + "18.age": "1.8.0"},
		},
		Spec: cnpgv1.ImageCatalogSpec{Images: []cnpgv1.CatalogImage{
			{Major: 17, Image: "pg:17", Extensions: []cnpgv1.ExtensionConfiguration{{Name: "pg-ivm"}}},
			{Major: 18, Image: "pg:18", Extensions: []cnpgv1.ExtensionConfiguration{
				{Name: "age", ImageVolumeSource: corev1.ImageVolumeSource{Reference: "registry.example/age:1.8.0"}},
				{Name: "wal2json"},
			}},
		}},
	}
	tests := []struct {
		name       string
		custom     *components.CNPGCustomSpec
		catalog    string
		want       ExtensionImages
		wantErr    bool
		noCatalogs bool
	}{
		{name: "no image extensions need no catalog", custom: withExtensions(components.ExtensionsSpec{PgVector: on}), noCatalogs: true},
		{
			name:   "entries and SQL versions",
			custom: withExtensions(components.ExtensionsSpec{AGE: on, Wal2JSON: on, PgVector: on}),
			want: ExtensionImages{
				"age":      {Config: catalog.Spec.Images[1].Extensions[0], SQLVersion: "1.8.0"},
				"wal2json": {Config: catalog.Spec.Images[1].Extensions[1]},
			},
		},
		{name: "missing for the major", custom: withExtensions(components.ExtensionsSpec{PgIVM: on}), wantErr: true},
		{name: "missing catalog", custom: withExtensions(components.ExtensionsSpec{AGE: on}), catalog: "other", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if !tt.noCatalogs {
				objects = append(objects, catalog.DeepCopy())
			}
			c, _ := newExtensionsTestContext(t, objects...)
			name := cmp.Or(tt.catalog, "extensions")
			got, err := ResolveExtensionImages(c, name, tt.custom, "18.4", "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveExtensionImages() error = %v, wantErr %t", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ResolveExtensionImages() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildExtensionsDatabase(t *testing.T) {
	if db := BuildExtensionsDatabase("my-pg", withExtensions(components.ExtensionsSpec{PgAudit: on, Wal2JSON: on}), nil); db != nil {
		t.Fatalf("expected no Database for extensions without CREATE EXTENSION, got %+v", db)
	}

	images := ExtensionImages{
		"postgis":   {SQLVersion: "3.6.4"},
		"pgrouting": {SQLVersion: "4.0.1"},
	}
	db := BuildExtensionsDatabase("my-pg", withExtensions(components.ExtensionsSpec{
		PgRouting: on, PostGIS: on, PgVector: on, PgAudit: on,
	}), images)
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
	db = BuildExtensionsDatabase("my-pg", custom, nil)
	if db.Spec.Name != "embeddings" || db.Spec.Owner != "ml" {
		t.Fatalf("unexpected identity: name=%q owner=%q", db.Spec.Name, db.Spec.Owner)
	}
}

func TestSyncExtensionsDatabase(t *testing.T) {
	c, cl := newExtensionsTestContext(t)
	if err := SyncExtensionsDatabase(c, withExtensions(components.ExtensionsSpec{PgVector: on}), nil); err != nil {
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
	if err := SyncExtensionsDatabase(c, nil, nil); err != nil {
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

