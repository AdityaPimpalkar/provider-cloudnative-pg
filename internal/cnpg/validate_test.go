package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestValidateScheduling(t *testing.T) {
	withAffinity := &components.CNPGCustomSpec{Affinity: &cnpgv1.AffinityConfiguration{}}
	spread := &[]corev1.TopologySpreadConstraint{{MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone}}

	tests := []struct {
		name    string
		policy  *commonv1alpha1.SchedulingPolicy
		custom  *components.CNPGCustomSpec
		wantErr bool
	}{
		{
			name:   "policy without parameters.affinity",
			policy: &commonv1alpha1.SchedulingPolicy{Affinity: &corev1.Affinity{}},
			custom: &components.CNPGCustomSpec{},
		},
		{
			name:   "parameters.affinity without policy",
			custom: withAffinity,
		},
		{
			name:   "parameters.affinity with scheduler and spread constraints",
			policy: &commonv1alpha1.SchedulingPolicy{SchedulerName: "custom", TopologySpreadConstraints: spread},
			custom: withAffinity,
		},
		{
			name:    "parameters.affinity with affinity",
			policy:  &commonv1alpha1.SchedulingPolicy{Affinity: &corev1.Affinity{}},
			custom:  withAffinity,
			wantErr: true,
		},
		{
			name:    "parameters.affinity with node selector",
			policy:  &commonv1alpha1.SchedulingPolicy{NodeSelector: map[string]string{"pool": "db"}},
			custom:  withAffinity,
			wantErr: true,
		},
		{
			name:    "parameters.affinity with tolerations",
			policy:  &commonv1alpha1.SchedulingPolicy{Tolerations: []corev1.Toleration{{Key: "db"}}},
			custom:  withAffinity,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateScheduling(tt.policy, tt.custom)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateScheduling() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
