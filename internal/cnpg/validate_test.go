package cnpg

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

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

func TestValidateEngineCPU(t *testing.T) {
	engine := func(request, limit string) corev1alpha1.ComponentSpec {
		replicas := int32(1)
		return corev1alpha1.ComponentSpec{
			Replicas: &replicas,
			Storage:  &corev1alpha1.Storage{Size: resource.MustParse("1Gi")},
			Resources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse(request), corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse(limit), corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
			},
		}
	}
	tests := []struct {
		request, limit string
		wantErr        string
	}{
		{request: "0.5", limit: "1"},
		{request: "500m", limit: "2.5"},
		{request: "0.001", limit: "1000"},
		{request: "-1", limit: "-1", wantErr: "greater than zero"},
		{request: "1", limit: "-2", wantErr: "greater than zero"},
		{request: "0.0005", limit: "1", wantErr: "finer than a millicore"},
		{request: "1", limit: "1.0001", wantErr: "finer than a millicore"},
		{request: "100u", limit: "1", wantErr: "finer than a millicore"},
		{request: "2", limit: "1", wantErr: "greater than limit"},
	}
	for _, tt := range tests {
		t.Run(tt.request+"/"+tt.limit, func(t *testing.T) {
			err := ValidateEngine(engine(tt.request, tt.limit))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateBootstrap(t *testing.T) {
	tests := []struct {
		name    string
		initDB  *cnpgv1.BootstrapInitDB
		wantErr string
	}{
		{name: "empty"},
		{name: "both set", initDB: &cnpgv1.BootstrapInitDB{Database: "app", Owner: "app"}},
		{name: "database only", initDB: &cnpgv1.BootstrapInitDB{Database: "app"}, wantErr: "both"},
		{name: "owner only", initDB: &cnpgv1.BootstrapInitDB{Owner: "app"}, wantErr: "both"},
		{name: "bad database", initDB: &cnpgv1.BootstrapInitDB{Database: "1app", Owner: "app"}, wantErr: "identifier"},
		{name: "bad owner", initDB: &cnpgv1.BootstrapInitDB{Database: "app", Owner: "app-user"}, wantErr: "identifier"},
		{name: "empty secret name", initDB: &cnpgv1.BootstrapInitDB{Secret: &cnpgv1.LocalObjectReference{Name: ""}}, wantErr: "secret name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBootstrap(&components.BootstrapConfiguration{InitDB: tt.initDB})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
