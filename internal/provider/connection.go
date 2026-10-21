package provider

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgpostgres "github.com/cloudnative-pg/cloudnative-pg/pkg/postgres"
	corev1 "k8s.io/api/core/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

const (
	// cnpgPostgresPortName is the Service port CloudNativePG creates
	// (pkg/specs.PostgresContainerName, used by buildInstanceServicePorts).
	cnpgPostgresPortName = "postgres"

	// libpq sslmodes. CloudNativePG forces ssl=on, and its default pg_hba rule
	// ("host all all all") still accepts unencrypted connections, which is
	// libpq's prefer. requireTLS inserts "hostnossl all all all reject".
	sslModePrefer  = "prefer"
	sslModeRequire = "require"

	// hostRProperty is the DNS name of the read Service (<cluster>-r).
	// That Service targets every ready instance, including the primary.
	hostRProperty = "host-r"
	// hostROProperty is the DNS name of the read-only Service (<cluster>-ro).
	// That Service targets ready replicas and excludes the primary.
	hostROProperty = "host-ro"
)

func connectionDetails(c *controller.Context, pg *cnpgv1.Cluster, requireTLS bool) (controller.ConnectionDetails, bool) {
	secretName, database, ok := connectionIdentity(pg)
	if !ok {
		return controller.ConnectionDetails{}, false
	}

	secret := &corev1.Secret{}
	if err := c.Get(secret, secretName); err != nil {
		return controller.ConnectionDetails{}, false
	}
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return controller.ConnectionDetails{}, false
	}

	host := serviceHost(pg.GetServiceReadWriteName(), c.Namespace())
	port := connectionPort(c, pg, secret)
	sslmode := clientSSLMode(requireTLS)
	extra := map[string]string{
		"database":   database,
		"secretName": secretName,
		"sslmode":    sslmode,
	}
	if pg.IsReadServiceEnabled() {
		extra[hostRProperty] = serviceHost(pg.GetServiceReadName(), c.Namespace())
	}
	if pg.IsReadOnlyServiceEnabled() {
		extra[hostROProperty] = serviceHost(pg.GetServiceReadOnlyName(), c.Namespace())
	}

	return controller.ConnectionDetails{
		Type:                 "postgresql",
		Provider:             common.ProviderName,
		Host:                 host,
		Port:                 port,
		Username:             username,
		Password:             password,
		URI:                  postgresURI(username, password, host, port, database, sslmode),
		AdditionalProperties: extra,
	}, true
}

func connectionIdentity(pg *cnpgv1.Cluster) (secretName, database string, ok bool) {
	if pg.ShouldCreateApplicationDatabase() {
		return pg.GetApplicationSecretName(), pg.GetApplicationDatabaseName(), true
	}
	if pg.GetEnableSuperuserAccess() {
		return pg.GetSuperuserSecretName(), "postgres", true
	}
	return "", "", false
}

func connectionPort(c *controller.Context, pg *cnpgv1.Cluster, secret *corev1.Secret) string {
	if port := servicePostgresPort(c, pg.GetServiceReadWriteName()); port != "" {
		return port
	}
	if port := string(secret.Data["port"]); port != "" {
		return port
	}
	return strconv.Itoa(cnpgpostgres.ServerPort)
}

func servicePostgresPort(c *controller.Context, name string) string {
	svc := &corev1.Service{}
	if err := c.Get(svc, name); err != nil {
		return ""
	}
	for _, port := range svc.Spec.Ports {
		if port.Name == cnpgPostgresPortName && port.Port > 0 {
			return strconv.Itoa(int(port.Port))
		}
	}
	return ""
}

func clientSSLMode(requireTLS bool) string {
	if requireTLS {
		return sslModeRequire
	}
	return sslModePrefer
}

func serviceHost(name, namespace string) string {
	return fmt.Sprintf("%s.%s.svc", name, namespace)
}

func postgresURI(user, password, host, port, database, sslmode string) string {
	u := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   database,
	}
	q := u.Query()
	q.Set("sslmode", sslmode)
	u.RawQuery = q.Encode()
	return u.String()
}
