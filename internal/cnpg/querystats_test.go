package cnpg

import (
	"slices"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

var off = &components.ExtensionToggle{Enabled: false}

func withParameters(custom *components.CNPGCustomSpec, parameters map[string]string) *components.CNPGCustomSpec {
	custom.PostgresConfiguration = &cnpgv1.PostgresConfiguration{Parameters: parameters}
	return custom
}

func TestDefaultPgStatStatements(t *testing.T) {
	clusterWith := func(parameters map[string]string) *cnpgv1.Cluster {
		pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"}}
		pg.Spec.PostgresConfiguration.Parameters = parameters
		return pg
	}
	tests := []struct {
		name    string
		custom  *components.CNPGCustomSpec
		cluster *cnpgv1.Cluster
		want    bool
	}{
		{name: "new instance", custom: &components.CNPGCustomSpec{}, want: true},
		{name: "new instance with other extensions", custom: withExtensions(components.ExtensionsSpec{PgVector: on}), want: true},
		{name: "existing instance running it", custom: &components.CNPGCustomSpec{}, cluster: clusterWith(map[string]string{"pg_stat_statements.track": "top"}), want: true},
		{name: "existing instance without it", custom: &components.CNPGCustomSpec{}, cluster: clusterWith(map[string]string{"work_mem": "8MB"}), want: false},
		{name: "existing instance tuned by the user", custom: withParameters(&components.CNPGCustomSpec{}, map[string]string{"pg_stat_statements.max": "10000"}), cluster: clusterWith(nil), want: true},
		{name: "explicitly off on a new instance", custom: withExtensions(components.ExtensionsSpec{PgStatStatements: off}), want: false},
		{name: "explicitly on for an existing instance", custom: withExtensions(components.ExtensionsSpec{PgStatStatements: on}), cluster: clusterWith(nil), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.cluster != nil {
				objects = append(objects, tt.cluster)
			}
			c, _ := newExtensionsTestContext(t, objects...)
			if err := DefaultPgStatStatements(c, tt.custom); err != nil {
				t.Fatal(err)
			}
			if got := PgStatStatementsEnabled(tt.custom); got != tt.want {
				t.Errorf("enabled = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestValidatePgStatStatements(t *testing.T) {
	tests := []struct {
		name    string
		custom  *components.CNPGCustomSpec
		wantErr bool
	}{
		{name: "unset"},
		{name: "on with tuning", custom: withParameters(withExtensions(components.ExtensionsSpec{PgStatStatements: on}), map[string]string{"pg_stat_statements.max": "10000"})},
		{name: "off", custom: withParameters(withExtensions(components.ExtensionsSpec{PgStatStatements: off}), map[string]string{"work_mem": "8MB"})},
		{name: "off with tuning", custom: withParameters(withExtensions(components.ExtensionsSpec{PgStatStatements: off}), map[string]string{"pg_stat_statements.max": "10000"}), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePgStatStatements(tt.custom); (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePgStatStatements() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestApplyExtensionsPgStatStatements(t *testing.T) {
	pg := &cnpgv1.Cluster{}
	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{PgStatStatements: on}), nil)
	if got := pg.Spec.PostgresConfiguration.Parameters["pg_stat_statements.track"]; got != "top" {
		t.Errorf("pg_stat_statements.track = %q, want top", got)
	}
	if pg.Spec.PostgresConfiguration.AdditionalLibraries != nil {
		t.Errorf("the operator preloads it, got shared_preload_libraries %v", pg.Spec.PostgresConfiguration.AdditionalLibraries)
	}

	pg = &cnpgv1.Cluster{}
	pg.Spec.PostgresConfiguration.Parameters = map[string]string{"pg_stat_statements.track": "all"}
	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{PgStatStatements: on}), nil)
	if got := pg.Spec.PostgresConfiguration.Parameters["pg_stat_statements.track"]; got != "all" {
		t.Errorf("pg_stat_statements.track = %q, want the user's all", got)
	}

	pg = &cnpgv1.Cluster{}
	ApplyExtensions(pg, withExtensions(components.ExtensionsSpec{PgStatStatements: off}), nil)
	if hasPgStatStatementsParameter(pg.Spec.PostgresConfiguration.Parameters) {
		t.Errorf("expected no pg_stat_statements parameters when off, got %v", pg.Spec.PostgresConfiguration.Parameters)
	}
}

func TestApplyQueryStatisticsMonitoring(t *testing.T) {
	ours := cnpgv1.ConfigMapKeySelector{
		LocalObjectReference: cnpgv1.LocalObjectReference{Name: "pg-pg-stat-statements"},
		Key:                  QueryStatisticsQueriesKey,
	}
	theirs := cnpgv1.ConfigMapKeySelector{LocalObjectReference: cnpgv1.LocalObjectReference{Name: "custom"}, Key: "queries"}

	userMonitoring := &cnpgv1.MonitoringConfiguration{CustomQueriesConfigMap: []cnpgv1.ConfigMapKeySelector{theirs}}
	pg := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg"}}
	pg.Spec.Monitoring = userMonitoring
	custom := withExtensions(components.ExtensionsSpec{PgStatStatements: on})
	ApplyQueryStatisticsMonitoring(pg, custom)
	ApplyQueryStatisticsMonitoring(pg, custom)
	if want := []cnpgv1.ConfigMapKeySelector{theirs, ours}; !slices.Equal(pg.Spec.Monitoring.CustomQueriesConfigMap, want) {
		t.Errorf("customQueriesConfigMap = %+v, want %+v", pg.Spec.Monitoring.CustomQueriesConfigMap, want)
	}
	if len(userMonitoring.CustomQueriesConfigMap) != 1 {
		t.Errorf("user monitoring mutated: %+v", userMonitoring.CustomQueriesConfigMap)
	}

	pg = &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg"}}
	ApplyQueryStatisticsMonitoring(pg, withExtensions(components.ExtensionsSpec{PgStatStatements: off}))
	if pg.Spec.Monitoring != nil {
		t.Errorf("expected no monitoring when off, got %+v", pg.Spec.Monitoring)
	}
}

// queryStatisticsQuery mirrors the fields CloudNativePG reads from a custom
// query (pkg/management/postgres/metrics.UserQuery).
type queryStatisticsQuery struct {
	Query          string                              `json:"query"`
	PredicateQuery string                              `json:"predicate_query"`
	CacheSeconds   uint64                              `json:"cache_seconds"`
	Metrics        []map[string]queryStatisticsMapping `json:"metrics"`
}

type queryStatisticsMapping struct {
	Usage       string `json:"usage"`
	Description string `json:"description"`
}

// The exporter rejects the whole ConfigMap if a query does not parse.
func TestQueryStatisticsQueriesParse(t *testing.T) {
	var queries map[string]queryStatisticsQuery
	if err := yaml.UnmarshalStrict([]byte(queryStatisticsQueries), &queries); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pg_stat_statements_top", "pg_stat_statements_database", "pg_stat_statements_info"} {
		query, ok := queries[name]
		if !ok {
			t.Errorf("missing query %s", name)
			continue
		}
		if query.Query == "" || query.PredicateQuery == "" {
			t.Errorf("%s: expected a query and a predicate on the extension", name)
		}
		for _, metric := range query.Metrics {
			for column, mapping := range metric {
				if !slices.Contains([]string{"LABEL", "COUNTER", "GAUGE"}, mapping.Usage) {
					t.Errorf("%s.%s: unexpected usage %q", name, column, mapping.Usage)
				}
			}
		}
	}
}

func TestQueryStatisticsConfigMapLifecycle(t *testing.T) {
	c, cl := newExtensionsTestContext(t)
	key := client.ObjectKey{Namespace: "ns", Name: QueryStatisticsConfigMapName("pg")}

	on := withExtensions(components.ExtensionsSpec{PgStatStatements: on})
	if err := ApplyQueryStatisticsConfigMap(c, on); err != nil {
		t.Fatal(err)
	}
	if err := DeleteQueryStatisticsConfigMap(c, on); err != nil {
		t.Fatal(err)
	}
	cm := &corev1.ConfigMap{}
	if err := cl.Get(t.Context(), key, cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if cm.Data[QueryStatisticsQueriesKey] != queryStatisticsQueries {
		t.Errorf("unexpected queries: %q", cm.Data[QueryStatisticsQueriesKey])
	}
	if _, ok := cm.Labels[reloadLabel]; !ok {
		t.Errorf("labels = %v, want %s", cm.Labels, reloadLabel)
	}
	if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].Name != "pg" {
		t.Errorf("owner references = %+v, want the Instance", cm.OwnerReferences)
	}

	off := withExtensions(components.ExtensionsSpec{PgStatStatements: off})
	if err := ApplyQueryStatisticsConfigMap(c, off); err != nil {
		t.Fatal(err)
	}
	if err := DeleteQueryStatisticsConfigMap(c, off); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(t.Context(), key, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected the ConfigMap deleted, got %v", err)
	}
	if err := DeleteQueryStatisticsConfigMap(c, off); err != nil {
		t.Errorf("deleting a missing ConfigMap: %v", err)
	}
}
