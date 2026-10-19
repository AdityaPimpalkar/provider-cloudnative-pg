package cnpg

import (
	"slices"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestApplyRequireTLS(t *testing.T) {
	userRules := []string{"host app app 10.0.0.0/8 scram-sha-256", "hostssl all all all cert"}
	tests := []struct {
		name       string
		requireTLS *bool
		userRules  []string
		want       []string
	}{
		{name: "unset keeps CNPG defaults", userRules: userRules, want: userRules},
		{name: "false keeps CNPG defaults", requireTLS: pointer.To(false), userRules: userRules, want: userRules},
		{name: "required without user rules", requireTLS: pointer.To(true), want: []string{RejectNonTLSRule}},
		{
			name: "required goes before user rules", requireTLS: pointer.To(true), userRules: userRules,
			want: append([]string{RejectNonTLSRule}, userRules...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &cnpgv1.Cluster{}
			pg.Spec.PostgresConfiguration.PgHBA = slices.Clone(tt.userRules)
			ApplyRequireTLS(pg, &components.CNPGCustomSpec{RequireTLS: tt.requireTLS})
			if !slices.Equal(pg.Spec.PostgresConfiguration.PgHBA, tt.want) {
				t.Fatalf("pg_hba = %q, want %q", pg.Spec.PostgresConfiguration.PgHBA, tt.want)
			}
		})
	}
}
