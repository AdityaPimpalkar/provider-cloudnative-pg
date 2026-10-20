package cnpg

import (
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
)

func ValidateCustomSpec(custom *components.CNPGCustomSpec) error {
	if custom.PriorityClassName != "" {
		if errs := validation.IsDNS1123Subdomain(custom.PriorityClassName); len(errs) > 0 {
			return fmt.Errorf("priorityClassName %q is invalid: %s", custom.PriorityClassName, strings.Join(errs, "; "))
		}
	}
	if custom.WALStorage != nil {
		if size := custom.WALStorage.GetSizeOrNil(); size == nil || size.Sign() <= 0 {
			return fmt.Errorf("walStorage requires a valid positive size or pvcTemplate storage request")
		}
	}
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
	if engine.Resources.Requests.Cpu().Cmp(*engine.Resources.Limits.Cpu()) > 0 {
		return fmt.Errorf("CPU request %s is greater than limit %s",
			engine.Resources.Requests.Cpu().String(),
			engine.Resources.Limits.Cpu().String())
	}
	if engine.Resources.Requests.Memory().Cmp(*engine.Resources.Limits.Memory()) > 0 {
		return fmt.Errorf("memory request %s is greater than limit %s",
			engine.Resources.Requests.Memory().String(),
			engine.Resources.Limits.Memory().String())
	}
	return nil
}

// ValidateScheduling rejects placing pods through both parameters.affinity and
// schedulingPolicy, as both write the Cluster's spec.affinity.
func ValidateScheduling(policy *commonv1alpha1.SchedulingPolicy, custom *components.CNPGCustomSpec) error {
	if policy == nil || custom.Affinity == nil {
		return nil
	}
	if policy.Affinity != nil || len(policy.NodeSelector) > 0 || len(policy.Tolerations) > 0 {
		return fmt.Errorf("parameters.affinity cannot be combined with schedulingPolicy affinity, nodeSelector or tolerations")
	}
	return nil
}

// ValidateSynchronous mirrors the CloudNativePG webhook so an invalid
// synchronous replication setting is reported before the Cluster is applied.
func ValidateSynchronous(engine corev1alpha1.ComponentSpec, custom *components.CNPGCustomSpec) error {
	if custom.PostgresConfiguration == nil || custom.PostgresConfiguration.Synchronous == nil || engine.Replicas == nil {
		return nil
	}
	sync := custom.PostgresConfiguration.Synchronous
	if sync.Number < 1 {
		return fmt.Errorf("postgresql.synchronous.number must be at least 1")
	}
	candidates := int(*engine.Replicas) + len(sync.StandbyNamesPre) + len(sync.StandbyNamesPost)
	if sync.Number >= candidates {
		return fmt.Errorf("postgresql.synchronous.number (%d) must be lower than the number of replicas (%d) plus listed standby names",
			sync.Number, *engine.Replicas)
	}
	return nil
}
