package cnpg

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

// RejectNonTLSRule refuses every TCP connection that is not encrypted. CNPG
// evaluates user rules after its fixed rules (local peer access and
// certificate-authenticated replication, both unaffected) and before its
// default "host all all all" rule, which this rule therefore shadows.
const RejectNonTLSRule = "hostnossl all all all reject"

// ApplyRequireTLS places RejectNonTLSRule ahead of the user's pg_hba rules
// when TLS is required.
func ApplyRequireTLS(pg *cnpgv1.Cluster, custom *components.CNPGCustomSpec) {
	if custom.RequireTLS == nil || !*custom.RequireTLS {
		return
	}
	pg.Spec.PostgresConfiguration.PgHBA = append([]string{RejectNonTLSRule}, pg.Spec.PostgresConfiguration.PgHBA...)
}
