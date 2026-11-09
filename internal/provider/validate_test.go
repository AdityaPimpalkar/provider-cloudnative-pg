package provider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

const (
	providerSpecPath = "../../charts/provider-cloudnative-pg/generated/provider-spec.yaml"
	instanceFixture  = "testdata/validate/instance.yaml"
)

// engineParameters sets the engine component's parameters.
func engineParameters(raw string) func(*corev1alpha1.Instance) {
	return func(in *corev1alpha1.Instance) {
		c := in.Spec.Components[common.ComponentEngine]
		c.Parameters = &runtime.RawExtension{Raw: []byte(raw)}
		in.Spec.Components[common.ComponentEngine] = c
	}
}

// TestValidate runs the checks the runtime runs at admission and before every
// Sync: the schemas, supportedFields and catalog the provider publishes, then
// Validate.
func TestValidate(t *testing.T) {
	t.Parallel()
	v := validationtest.New(t, New(), providerSpecPath)

	tests := []struct {
		name   string
		mutate func(*corev1alpha1.Instance)
		want   []string
	}{
		{name: "valid instance"},
		{
			name:   "missing engine",
			mutate: func(in *corev1alpha1.Instance) { delete(in.Spec.Components, common.ComponentEngine) },
			want:   []string{"spec.components[engine]"},
		},
		{
			name: "cpu request greater than limit",
			mutate: func(in *corev1alpha1.Instance) {
				in.Spec.Components[common.ComponentEngine].Resources.Requests[corev1.ResourceCPU] = resource.MustParse("2")
			},
			want: []string{"spec.components[engine]"},
		},
		{
			name:   "invalid priority class name",
			mutate: engineParameters(`{"priorityClassName":"Not_Valid"}`),
			want:   []string{"spec.components[engine].parameters"},
		},
		{
			name:   "logical decoding slots synchronized without HA slots",
			mutate: engineParameters(`{"replicationSlots":{"highAvailability":{"enabled":false,"synchronizeLogicalDecoding":true}}}`),
			want:   []string{"spec.components[engine].parameters.replicationSlots"},
		},
		{
			name:   "pg_stat_statements parameters with the extension off",
			mutate: engineParameters(`{"extensions":{"pgStatStatements":{"enabled":false}},"postgresql":{"parameters":{"pg_stat_statements.max":"1000"}}}`),
			want:   []string{"spec.components[engine].parameters.extensions.pgStatStatements"},
		},
		{
			name:   "synchronous standbys not fewer than replicas",
			mutate: engineParameters(`{"postgresql":{"synchronous":{"method":"any","number":3}}}`),
			want:   []string{"spec.components[engine].parameters.postgresql.synchronous"},
		},
		{
			name: "image-volume extension on PostgreSQL 17",
			mutate: func(in *corev1alpha1.Instance) {
				in.Spec.Version = "17.10"
				engineParameters(`{"extensions":{"postgis":{"enabled":true}}}`)(in)
			},
			want: []string{"spec.components[engine].parameters.extensions"},
		},
		{
			name: "proxy with an unsupported pool mode",
			mutate: func(in *corev1alpha1.Instance) {
				in.Spec.Components[common.ComponentProxy] = corev1alpha1.ComponentSpec{
					Replicas:   new(int32(1)),
					Parameters: &runtime.RawExtension{Raw: []byte(`{"poolMode":"statement"}`)},
				}
			},
			want: []string{"spec.components[proxy]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := validationtest.Load(t, instanceFixture)
			if tt.mutate != nil {
				tt.mutate(in)
			}
			got := v.Run(in, nil)
			var fields []string
			for _, err := range got.Errors {
				fields = append(fields, err.Field)
			}
			assert.Equal(t, tt.want, fields, "errors: %v", got.Errors)
		})
	}
}

// TestValidateUpdate checks the rules about changes, which need the stored
// Instance and so only run at admission.
func TestValidateUpdate(t *testing.T) {
	t.Parallel()
	v := validationtest.New(t, New(), providerSpecPath)

	tests := []struct {
		name         string
		old, updated string
		want         []string
		wantWarnings []string
	}{
		{
			name:    "pgvector turned off",
			old:     `{"extensions":{"pgvector":{"enabled":true}}}`,
			updated: `{"extensions":{"pgvector":{"enabled":false}}}`,
			want:    []string{"spec.components[engine].parameters.extensions.pgvector.enabled"},
		},
		{
			name:    "pgvector turned on, no restart",
			old:     `{}`,
			updated: `{"extensions":{"pgvector":{"enabled":true}}}`,
		},
		{
			name:         "pgaudit turned off restarts",
			old:          `{"extensions":{"pgaudit":{"enabled":true}}}`,
			updated:      `{}`,
			wantWarnings: []string{"spec.components[engine].parameters.extensions.pgaudit.enabled"},
		},
		{
			name:         "postgis turned on restarts",
			old:          `{}`,
			updated:      `{"extensions":{"postgis":{"enabled":true}}}`,
			wantWarnings: []string{"spec.components[engine].parameters.extensions.postgis.enabled"},
		},
		{
			name:    "AGE turned on after creation",
			old:     `{}`,
			updated: `{"extensions":{"age":{"enabled":true}}}`,
			want:    []string{"spec.components[engine].parameters.extensions.age.enabled"},
		},
		{
			name:         "TimescaleDB edition switched",
			old:          `{"extensions":{"timescaledb":{"enabled":true}}}`,
			updated:      `{"extensions":{"timescaledbCommunity":{"enabled":true}}}`,
			want:         []string{"spec.components[engine].parameters.extensions.timescaledbCommunity.enabled"},
			wantWarnings: []string{"spec.components[engine].parameters.extensions.timescaledb.enabled"},
		},
		{
			name:         "priority class changed",
			old:          `{}`,
			updated:      `{"priorityClassName":"high"}`,
			wantWarnings: []string{"spec.components[engine].parameters.priorityClassName"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			old := validationtest.Load(t, instanceFixture)
			old.Spec.Version = "18.6"
			engineParameters(tt.old)(old)
			updated := old.DeepCopy()
			engineParameters(tt.updated)(updated)

			got := v.Run(updated, old)
			var fields []string
			for _, err := range got.Errors {
				fields = append(fields, err.Field)
			}
			assert.Equal(t, tt.want, fields, "errors: %v", got.Errors)

			var warned []string
			for _, w := range got.Warnings {
				warned = append(warned, strings.SplitN(w, ":", 2)[0])
			}
			assert.Equal(t, tt.wantWarnings, warned, "warnings: %v", got.Warnings)
		})
	}
}
