package provider

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

const (
	// poolerNameSuffix keeps the Pooler name different from its Cluster.
	// The operator skips reconciliation when a Cluster of that name exists, and
	// its webhook rejects a Pooler named after the Cluster it references.
	// The Pooler's Service uses this same name. The operator's auth Secret is
	// also "<cluster>-pooler", which is a different kind and does not clash.
	poolerNameSuffix = "-pooler"

	// pgbouncerContainerName is the container the operator's deployment builder
	// looks up when it fills in image, command, and ports. Resources set on it
	// are kept.
	pgbouncerContainerName = "pgbouncer"
)

func poolerName(instance string) string {
	return instance + poolerNameSuffix
}

func proxyParameters(c *controller.Context, proxy corev1alpha1.ComponentSpec) (components.PgBouncerParameters, error) {
	var params components.PgBouncerParameters
	if proxy.Parameters == nil || len(proxy.Parameters.Raw) == 0 {
		return params, nil
	}
	if err := c.DecodeComponentParameters(proxy, &params); err != nil {
		return params, fmt.Errorf("failed to decode proxy parameters: %w", err)
	}
	return params, nil
}

func validateProxy(c *controller.Context) error {
	proxy, ok := c.Instance().Spec.Components[common.ComponentProxy]
	if !ok {
		return nil
	}
	if errs := validation.IsDNS1035Label(poolerName(c.Name())); len(errs) > 0 {
		return fmt.Errorf("proxy name %q is not a valid Service name: %s", poolerName(c.Name()), errs[0])
	}
	if proxy.Replicas != nil && *proxy.Replicas < 1 {
		return fmt.Errorf("proxy replicas must be at least 1")
	}
	params, err := proxyParameters(c, proxy)
	if err != nil {
		return err
	}
	switch cnpgv1.PgBouncerPoolMode(params.PoolMode) {
	case "", cnpgv1.PgBouncerPoolModeSession, cnpgv1.PgBouncerPoolModeTransaction:
		return nil
	default:
		return fmt.Errorf("proxy poolMode %q is invalid: CloudNativePG accepts %q or %q",
			params.PoolMode, cnpgv1.PgBouncerPoolModeSession, cnpgv1.PgBouncerPoolModeTransaction)
	}
}

// syncPooler creates the rw Pooler for the proxy component, or deletes it once
// the component is removed. The operator does not delete a Pooler with its Cluster.
func syncPooler(c *controller.Context) error {
	proxy, ok := c.Instance().Spec.Components[common.ComponentProxy]
	if !ok {
		return c.Delete(&cnpgv1.Pooler{ObjectMeta: c.ObjectMeta(poolerName(c.Name()))})
	}
	params, err := proxyParameters(c, proxy)
	if err != nil {
		return err
	}
	return c.Apply(buildPooler(c, proxy, params))
}

func buildPooler(c *controller.Context, proxy corev1alpha1.ComponentSpec, params components.PgBouncerParameters) *cnpgv1.Pooler {
	instances := int32(1)
	if proxy.Replicas != nil {
		instances = *proxy.Replicas
	}
	pooler := &cnpgv1.Pooler{
		ObjectMeta: c.ObjectMeta(poolerName(c.Name())),
		Spec: cnpgv1.PoolerSpec{
			Cluster:   cnpgv1.LocalObjectReference{Name: c.Name()},
			Type:      cnpgv1.PoolerTypeRW,
			Instances: &instances,
			// Leave authQuery unset so the operator keeps its automated integration
			// and creates the cnpg_pooler_pgbouncer role itself.
			PgBouncer: &cnpgv1.PgBouncerSpec{PoolMode: cnpgv1.PgBouncerPoolMode(params.PoolMode)},
			Template: &cnpgv1.PodTemplateSpec{
				ObjectMeta: cnpgv1.Metadata{Labels: c.PodLabels(common.ComponentProxy)},
			},
		},
	}
	if proxy.Resources != nil {
		// The operator requires containers when template.spec is set, and keeps
		// resources placed on the container it names pgbouncer.
		pooler.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:      pgbouncerContainerName,
			Resources: *proxy.Resources,
		}}
	}
	return pooler
}
