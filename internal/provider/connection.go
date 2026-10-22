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
	// sslModeRequire is the client sslmode published in connection details.
	// CloudNativePG always serves TLS, and the instance Service port is not
	// configurable, so require works whether or not requireTLS is set.
	// prefer is libpq's default, so publishing it adds nothing for libpq.
	// pg-connection-string >= 2.7 treats prefer, require, and verify-ca as
	// verify-full, which fails against the operator-managed server
	// certificate; the README says where the cluster CA lives.
	sslModeRequire = "require"

	// hostRProperty is the DNS name of the read Service (<cluster>-r).
	// That Service targets every ready instance, including the primary.
	hostRProperty = "host-r"
	// hostROProperty is the DNS name of the read-only Service (<cluster>-ro).
	// That Service targets ready replicas and excludes the primary, so a
	// cluster with fewer than two instances has no endpoints for it.
	hostROProperty = "host-ro"
)

func connectionDetails(c *controller.Context, pg *cnpgv1.Cluster) (controller.ConnectionDetails, bool) {
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
	port := connectionPort(secret)
	extra := map[string]string{
		"database":   database,
		"secretName": secretName,
		"sslmode":    sslModeRequire,
	}
	if pg.IsReadServiceEnabled() {
		extra[hostRProperty] = serviceHost(pg.GetServiceReadName(), c.Namespace())
	}
	// The -ro Service selects replicas only. One instance is the primary, so
	// that Service has no endpoints and is left out of the connection Secret.
	if pg.IsReadOnlyServiceEnabled() && pg.Spec.Instances >= 2 {
		extra[hostROProperty] = serviceHost(pg.GetServiceReadOnlyName(), c.Namespace())
	}

	return controller.ConnectionDetails{
		Type:                 "postgresql",
		Provider:             common.ProviderName,
		Host:                 host,
		Port:                 port,
		Username:             username,
		Password:             password,
		URI:                  postgresURI(username, password, host, port, database, sslModeRequire),
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

func connectionPort(secret *corev1.Secret) string {
	if port := string(secret.Data["port"]); port != "" {
		return port
	}
	return strconv.Itoa(cnpgpostgres.ServerPort)
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
