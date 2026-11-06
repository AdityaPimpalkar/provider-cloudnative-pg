package cnpg

import (
	"fmt"
	"maps"
	"regexp"
	"strconv"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	pgconfig "github.com/cloudnative-pg/cloudnative-pg/pkg/postgres"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

const (
	// defaultSlotPrefix is the CNPG default of highAvailability.slotPrefix.
	defaultSlotPrefix = "_cnpg_"

	paramHotStandbyFeedback   = "hot_standby_feedback"
	paramSyncReplicationSlots = "sync_replication_slots"

	// failoverSlotsExtension synchronizes logical slots before PostgreSQL 17.
	// CNPG preloads it when a pg_failover_slots.* parameter is set.
	failoverSlotsExtension = "pg_failover_slots"
)

// Matches the CNPG CRD pattern of highAvailability.slotPrefix.
var slotPrefixPattern = regexp.MustCompile(`^[0-9a-z_]*$`)

func synchronizeLogicalDecoding(custom *components.CNPGCustomSpec) bool {
	rs := custom.ReplicationSlots
	return rs != nil && rs.HighAvailability != nil && rs.HighAvailability.SynchronizeLogicalDecoding
}

func userParameters(custom *components.CNPGCustomSpec) map[string]string {
	if custom.PostgresConfiguration == nil {
		return nil
	}
	return custom.PostgresConfiguration.Parameters
}

// ApplyReplicationSlots sets the Cluster's replicationSlots. When logical
// decoding slots follow failover natively (PostgreSQL 17+ without
// pg_failover_slots), it turns on the settings CNPG requires for that, unless
// the user set them.
func ApplyReplicationSlots(pg *cnpgv1.Cluster, custom *components.CNPGCustomSpec, engineVersion, instanceVersion string) {
	if custom.ReplicationSlots == nil {
		return
	}
	pg.Spec.ReplicationSlots = custom.ReplicationSlots.DeepCopy()

	if !synchronizeLogicalDecoding(custom) || pgconfig.IsManagedExtensionUsed(failoverSlotsExtension, userParameters(custom)) {
		return
	}
	if major, err := strconv.Atoi(postgresMajor(engineVersion, instanceVersion)); err != nil || major < 17 {
		return
	}

	// Clone so the defaults never leak into the decoded custom spec.
	params := maps.Clone(pg.Spec.PostgresConfiguration.Parameters)
	if params == nil {
		params = map[string]string{}
	}
	for _, name := range []string{paramHotStandbyFeedback, paramSyncReplicationSlots} {
		if _, set := params[name]; !set {
			params[name] = "on"
		}
	}
	pg.Spec.PostgresConfiguration.Parameters = params
}

// ValidateReplicationSlots mirrors the CloudNativePG webhook so an invalid
// replication slots setting is reported before the Cluster is applied.
func ValidateReplicationSlots(custom *components.CNPGCustomSpec, engineVersion, instanceVersion string) error {
	rs := custom.ReplicationSlots
	if rs == nil {
		return nil
	}
	if ha := rs.HighAvailability; ha != nil && !slotPrefixPattern.MatchString(ha.SlotPrefix) {
		return fmt.Errorf("replicationSlots.highAvailability.slotPrefix %q may only contain lower case letters, numbers and underscores", ha.SlotPrefix)
	}
	if err := rs.SynchronizeReplicas.ValidateRegex(); err != nil {
		return fmt.Errorf("replicationSlots.synchronizeReplicas.excludePatterns: %w", err)
	}

	if !synchronizeLogicalDecoding(custom) {
		return nil
	}
	if !rs.HighAvailability.GetEnabled() {
		return fmt.Errorf("replicationSlots.highAvailability.synchronizeLogicalDecoding requires replicationSlots.highAvailability.enabled")
	}
	params := userParameters(custom)
	if pgconfig.IsManagedExtensionUsed(failoverSlotsExtension, params) {
		return nil
	}
	major, err := strconv.Atoi(postgresMajor(engineVersion, instanceVersion))
	if err != nil {
		return fmt.Errorf("cannot determine the PostgreSQL major version from %q", postgresVersion(engineVersion, instanceVersion))
	}
	if major < 17 {
		return fmt.Errorf("replicationSlots.highAvailability.synchronizeLogicalDecoding requires PostgreSQL 17 or later, " +
			"or the pg_failover_slots extension (set a pg_failover_slots.* parameter in postgresql.parameters)")
	}
	for _, name := range []string{paramHotStandbyFeedback, paramSyncReplicationSlots} {
		value, set := params[name]
		if !set {
			continue
		}
		if on, err := pgconfig.ParsePostgresConfigBoolean(value); err != nil || !on {
			return fmt.Errorf("postgresql.parameters.%s must be on (or unset) when replicationSlots.highAvailability.synchronizeLogicalDecoding is enabled", name)
		}
	}
	return nil
}

// ValidateReplicationSlotsChange rejects changing the HA slot prefix while the
// live Cluster has HA slots enabled, which CloudNativePG refuses: the operator
// needs the old prefix to clean the slots up. Removing the setting counts as a
// change back to the default prefix.
func ValidateReplicationSlotsChange(c *controller.Context, custom *components.CNPGCustomSpec) error {
	pg := &cnpgv1.Cluster{}
	if err := c.Get(pg, c.Name()); err != nil {
		if controller.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Cluster: %w", err)
	}
	live := pg.Spec.ReplicationSlots
	if live == nil || !live.HighAvailability.GetEnabled() {
		return nil
	}

	current, desired := defaultSlotPrefix, defaultSlotPrefix
	if live.HighAvailability.SlotPrefix != "" {
		current = live.HighAvailability.SlotPrefix
	}
	if rs := custom.ReplicationSlots; rs != nil && rs.HighAvailability != nil && rs.HighAvailability.SlotPrefix != "" {
		desired = rs.HighAvailability.SlotPrefix
	}
	if current != desired {
		return fmt.Errorf("replicationSlots.highAvailability.slotPrefix cannot change from %q to %q while HA replication slots are enabled", current, desired)
	}
	return nil
}
