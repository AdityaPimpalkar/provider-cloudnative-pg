package cnpg

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
)

func ValidateCustomSpec(custom *components.PostgresqlCustomSpec) error {
	if custom.Bootstrap != nil {
		if err := ValidateBootstrap(custom.Bootstrap); err != nil {
			return err
		}
	}
	if custom.Certificates != nil {
		if err := ValidateCertificates(custom.Certificates); err != nil {
			return err
		}
	}
	return nil
}

func ValidateCertificates(certificates *cnpgv1.CertificatesConfiguration) error {
	if certificates.ServerTLSSecret != "" {
		if len(certificates.ServerAltDNSNames) != 0 {
			return fmt.Errorf("server alternative DNS names cannot be specified when server TLS secret is provided")
		}
		if certificates.ServerCASecret == "" {
			return fmt.Errorf("server CA secret is required when server TLS secret is provided")
		}
	}

	if certificates.ReplicationTLSSecret != "" && certificates.ClientCASecret == "" {
		return fmt.Errorf("client CA secret is required when replication TLS secret is provided")
	}

	return nil
}

func ValidateBootstrap(bootstrap *components.BootstrapConfiguration) error {
	if bootstrap.InitDB == nil {
		return nil
	}

	initDB := bootstrap.InitDB
	if (initDB.Database != "" && initDB.Owner == "") || (initDB.Database == "" && initDB.Owner != "") {
		return fmt.Errorf("bootstrap initdb database and owner must both be specified or both left empty")
	}
	if initDB.Secret != nil && initDB.Secret.Name == "" {
		return fmt.Errorf("bootstrap initdb secret name cannot be empty")
	}

	return nil
}

func ValidateEngine(engine corev1alpha1.ComponentSpec) error {
	if engine.Replicas == nil {
		return fmt.Errorf("replicas is required")
	}
	if int(*engine.Replicas) < 1 {
		return fmt.Errorf("replicas must be at least 1")
	}
	if engine.Storage == nil || engine.Storage.Size.String() == "" {
		return fmt.Errorf("storage size is required")
	}
	if engine.Resources == nil {
		return fmt.Errorf("resources are required")
	}
	if engine.Resources.Requests.Cpu().IsZero() || engine.Resources.Requests.Memory().IsZero() {
		return fmt.Errorf("both CPU and memory requests are required")
	}
	if engine.Resources.Limits.Cpu().IsZero() || engine.Resources.Limits.Memory().IsZero() {
		return fmt.Errorf("both CPU and memory limits are required")
	}
	return nil
}
