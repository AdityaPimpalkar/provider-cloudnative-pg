package provider

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
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
)

func poolerName(instance string) string {
	return instance + poolerNameSuffix
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
