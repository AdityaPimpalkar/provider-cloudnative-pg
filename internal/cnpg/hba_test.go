package cnpg

import (
	"slices"
	"strings"
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestParsePgHBA(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []HBARule
	}{
		{name: "empty", text: ""},
		{name: "blank lines and comments", text: "\n  # only a comment\n\t\n"},
		{
			name: "records in order",
			text: `
# orders service from the app network
hostssl orders orders_app 10.0.0.0/8 scram-sha-256   # trailing comment
host    orders all        all        reject
`,
			want: []HBARule{
				{Line: "hostssl orders orders_app 10.0.0.0/8 scram-sha-256", Connection: "hostssl", Method: "scram-sha-256"},
				{Line: "host    orders all        all        reject", Connection: "host", Method: "reject"},
			},
		},
		{
			name: "ip address with netmask",
			text: "host all all 192.168.1.0 255.255.255.0 md5",
			want: []HBARule{{Line: "host all all 192.168.1.0 255.255.255.0 md5", Connection: "host", Method: "md5"}},
		},
		{
			name: "ipv6 cidr, lists and quoted names",
			text: `hostnossl "my db",app sales,"Bob Smith" ::1/128 reject`,
			want: []HBARule{{Line: `hostnossl "my db",app sales,"Bob Smith" ::1/128 reject`, Connection: "hostnossl", Method: "reject"}},
		},
		{
			name: "host name and domain suffix",
			text: "hostssl all all .example.com scram-sha-256\nhostssl all all db-client.svc scram-sha-256",
			want: []HBARule{
				{Line: "hostssl all all .example.com scram-sha-256", Connection: "hostssl", Method: "scram-sha-256"},
				{Line: "hostssl all all db-client.svc scram-sha-256", Connection: "hostssl", Method: "scram-sha-256"},
			},
		},
		{
			name: "cert with options",
			text: "hostssl all all samenet cert clientcert=verify-full map=certs",
			want: []HBARule{{Line: "hostssl all all samenet cert clientcert=verify-full map=certs", Connection: "hostssl", Method: "cert"}},
		},
		{
			name: "ldap with a quoted option holding # and blanks",
			text: `hostssl all all all ldap ldapserver=ldap.example.com ldapbinddn="cn=svc #1, dc=example" # comment`,
			want: []HBARule{{
				Line:       `hostssl all all all ldap ldapserver=ldap.example.com ldapbinddn="cn=svc #1, dc=example"`,
				Connection: "hostssl", Method: "ldap",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePgHBA(tt.text)
			if err != nil {
				t.Fatalf("ParsePgHBA() error = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ParsePgHBA() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParsePgHBAErrors(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "trust", text: "host all all all trust", want: "line 1: method trust is not allowed"},
		{name: "reports the line number", text: "# ok\nhostssl all all all scram-sha-256\nhost all all all password", want: `line 3: unsupported authentication method "password"`},
		{name: "local", text: "local all all peer", want: "local records are not supported"},
		{name: "include", text: "include_dir conf.d", want: "include_dir directives are not supported"},
		{name: "unknown type", text: "hostgssenc all all all scram-sha-256", want: `unsupported connection type "hostgssenc"`},
		{name: "too few fields", text: "host all all scram-sha-256", want: "expected"},
		{name: "missing method after netmask", text: "host all all 10.0.0.0 255.0.0.0", want: "missing authentication method"},
		{name: "ip without mask", text: "host all all 10.0.0.1 scram-sha-256", want: "needs a CIDR suffix"},
		{name: "bad cidr", text: "host all all 10.0.0.0/33 scram-sha-256", want: "invalid address"},
		{name: "bad address", text: "host all all ${podselector:app} scram-sha-256", want: "invalid address"},
		{name: "file reference", text: "host all @admins all scram-sha-256", want: "user file references"},
		{name: "empty list item", text: "host app, all all scram-sha-256", want: "empty database name"},
		{name: "cert without ssl", text: "host all all all cert", want: "method cert requires a hostssl record"},
		{name: "bad option", text: "hostssl all all all ldap ldapserver", want: `option "ldapserver" must be NAME=VALUE`},
		{name: "unterminated quote", text: `host "app all all reject`, want: "unterminated quoted string"},
		{name: "continuation", text: `host all all all reject \`, want: "line continuations are not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePgHBA(tt.text)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParsePgHBA() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestApplyPgHBA(t *testing.T) {
	tests := []struct {
		name       string
		custom     components.CNPGCustomSpec
		passthrough []string
		want       []string
	}{
		{name: "unset keeps postgresql.pg_hba", passthrough: []string{"host all all all reject"}, want: []string{"host all all all reject"}},
		{
			name:   "records without comments",
			custom: components.CNPGCustomSpec{PgHBA: "# app\nhostssl app app 10.0.0.0/8 scram-sha-256 # app pods\n\nhost app all all reject\n"},
			want:   []string{"hostssl app app 10.0.0.0/8 scram-sha-256", "host app all all reject"},
		},
		{
			name:   "requireTLS goes first",
			custom: components.CNPGCustomSpec{PgHBA: "host app app 10.0.0.0/8 scram-sha-256", RequireTLS: pointer.To(true)},
			want:   []string{RejectNonTLSRule, "host app app 10.0.0.0/8 scram-sha-256"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &cnpgv1.Cluster{}
			pg.Spec.PostgresConfiguration.PgHBA = slices.Clone(tt.passthrough)
			if err := ApplyPgHBA(pg, &tt.custom); err != nil {
				t.Fatalf("ApplyPgHBA() error = %v", err)
			}
			ApplyRequireTLS(pg, &tt.custom)
			if !slices.Equal(pg.Spec.PostgresConfiguration.PgHBA, tt.want) {
				t.Fatalf("pg_hba = %q, want %q", pg.Spec.PostgresConfiguration.PgHBA, tt.want)
			}
		})
	}
}

func TestValidateCustomSpecPgHBA(t *testing.T) {
	valid := "hostssl app app 10.0.0.0/8 scram-sha-256"
	tests := []struct {
		name    string
		custom  components.CNPGCustomSpec
		wantErr string
	}{
		{name: "valid", custom: components.CNPGCustomSpec{PgHBA: valid}},
		{name: "postgresql.pg_hba alone", custom: components.CNPGCustomSpec{
			PostgresConfiguration: &cnpgv1.PostgresConfiguration{PgHBA: []string{"host all all all trust"}},
		}},
		{name: "invalid record", custom: components.CNPGCustomSpec{PgHBA: "host all all all trust"}, wantErr: "method trust is not allowed"},
		{name: "combined with postgresql.pg_hba", custom: components.CNPGCustomSpec{
			PgHBA:                 valid,
			PostgresConfiguration: &cnpgv1.PostgresConfiguration{PgHBA: []string{valid}},
		}, wantErr: "pgHBA cannot be combined with postgresql.pg_hba"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCustomSpec(&tt.custom)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateCustomSpec() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateCustomSpec() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
