package cnpg

import (
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func withSlots(slots *cnpgv1.ReplicationSlotsConfiguration, params map[string]string) *components.CNPGCustomSpec {
	custom := &components.CNPGCustomSpec{ReplicationSlots: slots}
	if params != nil {
		custom.PostgresConfiguration = &cnpgv1.PostgresConfiguration{Parameters: params}
	}
	return custom
}

func logicalFailover(ha *cnpgv1.ReplicationSlotsHAConfiguration) *cnpgv1.ReplicationSlotsConfiguration {
	if ha == nil {
		ha = &cnpgv1.ReplicationSlotsHAConfiguration{}
	}
	ha.SynchronizeLogicalDecoding = true
	return &cnpgv1.ReplicationSlotsConfiguration{HighAvailability: ha}
}

func TestValidateReplicationSlots(t *testing.T) {
	tests := []struct {
		name    string
		custom  *components.CNPGCustomSpec
		version string
		wantErr bool
	}{
		{name: "unset", custom: &components.CNPGCustomSpec{}, version: "16.14"},
		{
			name:    "disable HA slots",
			custom:  withSlots(&cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{Enabled: pointer.To(false)}}, nil),
			version: "16.14",
		},
		{
			name:    "valid prefix",
			custom:  withSlots(&cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{SlotPrefix: "ha_1_"}}, nil),
			version: "18.4",
		},
		{
			name:    "invalid prefix",
			custom:  withSlots(&cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{SlotPrefix: "HA-"}}, nil),
			version: "18.4",
			wantErr: true,
		},
		{
			name: "invalid exclude pattern",
			custom: withSlots(&cnpgv1.ReplicationSlotsConfiguration{SynchronizeReplicas: &cnpgv1.SynchronizeReplicasConfiguration{
				Enabled: pointer.To(true), ExcludePatterns: []string{"^ok$", "("},
			}}, nil),
			version: "18.4",
			wantErr: true,
		},
		{name: "logical failover on 17", custom: withSlots(logicalFailover(nil), nil), version: "17.10"},
		{name: "logical failover on 18", custom: withSlots(logicalFailover(nil), nil), version: "18.4"},
		{name: "logical failover on 16", custom: withSlots(logicalFailover(nil), nil), version: "16.14", wantErr: true},
		{
			name:    "logical failover on 16 with pg_failover_slots",
			custom:  withSlots(logicalFailover(nil), map[string]string{"pg_failover_slots.synchronize_slot_names": "name_like:%"}),
			version: "16.14",
		},
		{
			name:    "logical failover without HA slots",
			custom:  withSlots(logicalFailover(&cnpgv1.ReplicationSlotsHAConfiguration{Enabled: pointer.To(false)}), nil),
			version: "18.4",
			wantErr: true,
		},
		{
			name:    "hot_standby_feedback explicitly on",
			custom:  withSlots(logicalFailover(nil), map[string]string{"hot_standby_feedback": "true"}),
			version: "18.4",
		},
		{
			name:    "hot_standby_feedback explicitly off",
			custom:  withSlots(logicalFailover(nil), map[string]string{"hot_standby_feedback": "off"}),
			version: "18.4",
			wantErr: true,
		},
		{
			name:    "sync_replication_slots explicitly off",
			custom:  withSlots(logicalFailover(nil), map[string]string{"sync_replication_slots": "0"}),
			version: "17.10",
			wantErr: true,
		},
		{
			name:    "hot_standby_feedback off is irrelevant without logical failover",
			custom:  withSlots(&cnpgv1.ReplicationSlotsConfiguration{}, map[string]string{"hot_standby_feedback": "off"}),
			version: "18.4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateReplicationSlots(tt.custom, tt.version, ""); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestApplyReplicationSlots(t *testing.T) {
	tests := []struct {
		name       string
		custom     *components.CNPGCustomSpec
		version    string
		wantParams map[string]string
	}{
		{name: "unset", custom: &components.CNPGCustomSpec{}, version: "18.4"},
		{
			name:    "HA settings only",
			custom:  withSlots(&cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{SlotPrefix: "ha_"}}, nil),
			version: "18.4",
		},
		{
			name:       "logical failover turns on the required settings",
			custom:     withSlots(logicalFailover(nil), map[string]string{"max_connections": "200"}),
			version:    "17.10",
			wantParams: map[string]string{"max_connections": "200", "hot_standby_feedback": "on", "sync_replication_slots": "on"},
		},
		{
			name:       "explicit settings are kept",
			custom:     withSlots(logicalFailover(nil), map[string]string{"hot_standby_feedback": "true"}),
			version:    "18.4",
			wantParams: map[string]string{"hot_standby_feedback": "true", "sync_replication_slots": "on"},
		},
		{
			name:       "pg_failover_slots is left alone",
			custom:     withSlots(logicalFailover(nil), map[string]string{"pg_failover_slots.synchronize_slot_names": "name_like:%"}),
			version:    "16.14",
			wantParams: map[string]string{"pg_failover_slots.synchronize_slot_names": "name_like:%"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &cnpgv1.Cluster{}
			if tt.custom.PostgresConfiguration != nil {
				pg.Spec.PostgresConfiguration = *tt.custom.PostgresConfiguration
			}
			userParams := map[string]string{}
			for k, v := range userParameters(tt.custom) {
				userParams[k] = v
			}

			ApplyReplicationSlots(pg, tt.custom, tt.version, "")

			if tt.custom.ReplicationSlots == nil {
				if pg.Spec.ReplicationSlots != nil {
					t.Fatalf("replicationSlots = %+v, want unset", pg.Spec.ReplicationSlots)
				}
			} else if pg.Spec.ReplicationSlots == nil || *pg.Spec.ReplicationSlots.HighAvailability != *tt.custom.ReplicationSlots.HighAvailability {
				t.Fatalf("replicationSlots = %+v, want %+v", pg.Spec.ReplicationSlots, tt.custom.ReplicationSlots)
			}
			if len(pg.Spec.PostgresConfiguration.Parameters) != len(tt.wantParams) {
				t.Fatalf("parameters = %v, want %v", pg.Spec.PostgresConfiguration.Parameters, tt.wantParams)
			}
			for k, v := range tt.wantParams {
				if got := pg.Spec.PostgresConfiguration.Parameters[k]; got != v {
					t.Fatalf("parameters[%s] = %q, want %q", k, got, v)
				}
			}
			if got := userParameters(tt.custom); len(got) != len(userParams) {
				t.Fatalf("user parameters mutated: %v, was %v", got, userParams)
			}
		})
	}
}

func TestValidateReplicationSlotsChange(t *testing.T) {
	liveCluster := func(slots *cnpgv1.ReplicationSlotsConfiguration) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: "ns"},
			Spec:       cnpgv1.ClusterSpec{ReplicationSlots: slots},
		}
	}
	defaulted := &cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{
		Enabled: pointer.To(true), SlotPrefix: "_cnpg_",
	}}
	custom := &cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{
		Enabled: pointer.To(true), SlotPrefix: "ha_",
	}}
	disabledCustom := &cnpgv1.ReplicationSlotsConfiguration{HighAvailability: &cnpgv1.ReplicationSlotsHAConfiguration{
		Enabled: pointer.To(false), SlotPrefix: "ha_",
	}}

	tests := []struct {
		name    string
		live    *cnpgv1.Cluster
		desired *cnpgv1.ReplicationSlotsConfiguration
		wantErr bool
	}{
		{name: "no Cluster yet", desired: custom},
		{name: "default prefix kept", live: liveCluster(defaulted)},
		{name: "default prefix kept with other settings", live: liveCluster(defaulted), desired: logicalFailover(nil)},
		{name: "custom prefix kept", live: liveCluster(custom), desired: custom},
		{name: "prefix changed", live: liveCluster(defaulted), desired: custom, wantErr: true},
		{name: "custom prefix removed", live: liveCluster(custom), wantErr: true},
		{name: "prefix changed while HA slots are disabled", live: liveCluster(disabledCustom), desired: defaulted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			if tt.live != nil {
				objects = append(objects, tt.live)
			}
			c, _ := newExtensionsTestContext(t, objects...)
			if err := ValidateReplicationSlotsChange(c, withSlots(tt.desired, nil)); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
