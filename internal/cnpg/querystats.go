package cnpg

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

// pgStatStatementsParameterPrefix: CloudNativePG preloads pg_stat_statements
// and creates it in every database while a parameter with this prefix is set.
const pgStatStatementsParameterPrefix = "pg_stat_statements."

// QueryStatisticsQueriesKey is the key of the exporter queries in the ConfigMap.
const QueryStatisticsQueriesKey = "queries"

// reloadLabel makes the instances reload the queries when the ConfigMap changes.
const reloadLabel = "cnpg.io/reload"

//go:embed pg_stat_statements_queries.yaml
var queryStatisticsQueries string

// QueryStatisticsConfigMapName is the owned ConfigMap with the exporter queries
// for pg_stat_statements.
func QueryStatisticsConfigMapName(instanceName string) string {
	return instanceName + "-pg-stat-statements"
}

func hasPgStatStatementsParameter(parameters map[string]string) bool {
	for name := range parameters {
		if strings.HasPrefix(name, pgStatStatementsParameterPrefix) {
			return true
		}
	}
	return false
}

// PgStatStatementsEnabled reports whether pg_stat_statements is on, after
// DefaultPgStatStatements resolved an unset toggle.
func PgStatStatementsEnabled(custom *components.CNPGCustomSpec) bool {
	return custom != nil && custom.Extensions != nil &&
		custom.Extensions.PgStatStatements != nil && custom.Extensions.PgStatStatements.Enabled
}

// DefaultPgStatStatements resolves an unset extensions.pgStatStatements: on for
// a new instance or when pg_stat_statements.* parameters are set (the operator
// enables it for them), and otherwise whatever the existing Cluster runs, so a
// provider upgrade does not restart instances to turn it on.
func DefaultPgStatStatements(c *controller.Context, custom *components.CNPGCustomSpec) error {
	if custom.Extensions != nil && custom.Extensions.PgStatStatements != nil {
		return nil
	}

	enabled := true
	if custom.PostgresConfiguration == nil || !hasPgStatStatementsParameter(custom.PostgresConfiguration.Parameters) {
		existing := &cnpgv1.Cluster{}
		if err := c.Get(existing, c.Name()); err == nil {
			enabled = hasPgStatStatementsParameter(existing.Spec.PostgresConfiguration.Parameters)
		} else if !controller.IsNotFound(err) {
			return fmt.Errorf("get Cluster: %w", err)
		}
	}

	if custom.Extensions == nil {
		custom.Extensions = &components.ExtensionsSpec{}
	}
	custom.Extensions.PgStatStatements = &components.ExtensionToggle{Enabled: enabled}
	return nil
}

// ValidatePgStatStatements rejects pg_stat_statements.* parameters while the
// extension is turned off: the operator would enable it anyway.
func ValidatePgStatStatements(custom *components.CNPGCustomSpec) error {
	if custom == nil || custom.Extensions == nil || custom.Extensions.PgStatStatements == nil ||
		custom.Extensions.PgStatStatements.Enabled || custom.PostgresConfiguration == nil {
		return nil
	}
	if hasPgStatStatementsParameter(custom.PostgresConfiguration.Parameters) {
		return fmt.Errorf("%s* parameters enable pg_stat_statements; remove them or set extensions.pgStatStatements.enabled to true",
			pgStatStatementsParameterPrefix)
	}
	return nil
}

// BuildQueryStatisticsConfigMap builds the ConfigMap with the exporter queries
// for pg_stat_statements; the caller sets its metadata.
func BuildQueryStatisticsConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{Data: map[string]string{QueryStatisticsQueriesKey: queryStatisticsQueries}}
}

// ApplyQueryStatisticsMonitoring adds the pg_stat_statements queries to the
// exporter of the Cluster while the extension is on.
func ApplyQueryStatisticsMonitoring(pg *cnpgv1.Cluster, custom *components.CNPGCustomSpec) {
	if !PgStatStatementsEnabled(custom) {
		return
	}
	selector := cnpgv1.ConfigMapKeySelector{
		LocalObjectReference: cnpgv1.LocalObjectReference{Name: QueryStatisticsConfigMapName(pg.Name)},
		Key:                  QueryStatisticsQueriesKey,
	}
	// Copy: Monitoring may point into the decoded Instance parameters.
	monitoring := &cnpgv1.MonitoringConfiguration{}
	if pg.Spec.Monitoring != nil {
		monitoring = pg.Spec.Monitoring.DeepCopy()
	}
	if !slices.Contains(monitoring.CustomQueriesConfigMap, selector) {
		monitoring.CustomQueriesConfigMap = append(monitoring.CustomQueriesConfigMap, selector)
	}
	pg.Spec.Monitoring = monitoring
}

// ApplyQueryStatisticsConfigMap creates or updates the ConfigMap with the
// exporter queries while pg_stat_statements is on. Call it before applying the
// Cluster that references it.
func ApplyQueryStatisticsConfigMap(c *controller.Context, custom *components.CNPGCustomSpec) error {
	if !PgStatStatementsEnabled(custom) {
		return nil
	}
	cm := BuildQueryStatisticsConfigMap()
	cm.ObjectMeta = c.ObjectMeta(QueryStatisticsConfigMapName(c.Name()))
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels[reloadLabel] = ""
	if err := c.Apply(cm); err != nil {
		return fmt.Errorf("apply pg_stat_statements queries ConfigMap: %w", err)
	}
	return nil
}

// DeleteQueryStatisticsConfigMap removes the ConfigMap once pg_stat_statements
// is off. Call it after applying the Cluster that no longer references it.
func DeleteQueryStatisticsConfigMap(c *controller.Context, custom *components.CNPGCustomSpec) error {
	if PgStatStatementsEnabled(custom) {
		return nil
	}
	if err := c.Delete(&corev1.ConfigMap{ObjectMeta: c.ObjectMeta(QueryStatisticsConfigMapName(c.Name()))}); err != nil {
		return fmt.Errorf("delete pg_stat_statements queries ConfigMap: %w", err)
	}
	return nil
}
