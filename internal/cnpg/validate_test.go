package cnpg

import (
	"os"
	"regexp"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

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

// The UI regex and validateCPU must agree on values written in the notation the UI accepts.
func TestCPURuleMatchesUIRegex(t *testing.T) {
	raw, err := os.ReadFile("../../definition/topologies/replicaSet/topology.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var topology struct {
		UI map[string]any `yaml:"ui"`
	}
	if err := yaml.Unmarshal(raw, &topology); err != nil {
		t.Fatal(err)
	}
	patterns := map[string]bool{}
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if path, _ := v["path"].(string); strings.HasSuffix(path, ".cpu") {
				validation, _ := v["validation"].(map[string]any)
				regex, _ := validation["regex"].(map[string]any)
				pattern, _ := regex["pattern"].(string)
				if pattern == "" {
					t.Fatalf("CPU field %s has no regex", path)
				}
				patterns[pattern] = true
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(topology.UI)
	if len(patterns) != 1 {
		t.Fatalf("want one shared CPU regex, found %d", len(patterns))
	}
	for pattern := range patterns {
		uiRule := regexp.MustCompile(pattern)
		for _, value := range []string{
			"1", "2", "10", "0.5", ".5", "1.25", "2.125", "0.001", "500m", "1m", "1500m",
			"0", "0.0", "0m", "-1", "-500m", "0.0005", "1.0001",
		} {
			uiOK := uiRule.MatchString(value)
			goOK := validateCPU("request", resource.MustParse(value)) == nil
			if uiOK != goOK {
				t.Errorf("%q: UI regex accepts=%t, Validate accepts=%t", value, uiOK, goOK)
			}
		}
	}
}
