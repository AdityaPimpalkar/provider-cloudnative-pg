package provider

import (
	"context"
	"fmt"
	"strconv"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	pgbouncerconfig "github.com/cloudnative-pg/cloudnative-pg/pkg/management/pgbouncer/config"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

	// pgbouncerClientTLSSSLMode is the PgBouncer setting for client-side TLS.
	// The operator defaults it to prefer, which accepts plaintext clients.
	pgbouncerClientTLSSSLMode = "client_tls_sslmode"
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
	if proxy.Type != "" && proxy.Type != common.ComponentTypePgbouncer {
		return fmt.Errorf("proxy type %q is not supported: use %q", proxy.Type, common.ComponentTypePgbouncer)
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
// With requireTLS the pooler refuses plaintext clients too, since clients
// reach PostgreSQL through it.
func syncPooler(c *controller.Context, requireTLS bool) error {
	proxy, ok := c.Instance().Spec.Components[common.ComponentProxy]
	if !ok {
		return deletePooler(c)
	}
	params, err := proxyParameters(c, proxy)
	if err != nil {
		return err
	}
	return c.Apply(buildPooler(c, proxy, params, requireTLS))
}

func deletePooler(c *controller.Context) error {
	exists, err := c.Exists(&cnpgv1.Pooler{}, poolerName(c.Name()))
	if err != nil || !exists {
		return err
	}
	return c.Delete(&cnpgv1.Pooler{ObjectMeta: c.ObjectMeta(poolerName(c.Name()))})
}

func buildPooler(c *controller.Context, proxy corev1alpha1.ComponentSpec, params components.PgBouncerParameters, requireTLS bool) *cnpgv1.Pooler {
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
	if requireTLS {
		pooler.Spec.PgBouncer.Parameters = map[string]string{pgbouncerClientTLSSSLMode: "require"}
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

// poolerBlocksReady keeps the Instance from becoming Ready until the Pooler is
// serving, and points the primary connection at the pooler's Service.
//
// The operator sets phase Active once the Pooler's prerequisites exist, and its
// status.instances counts pods created, not pods ready. Readiness therefore
// comes from the Deployment the operator names after the Pooler: its Service
// only routes to ready pods. host-r and host-ro stay on the PostgreSQL
// Services: this Pooler is type rw and does not front them.
func poolerBlocksReady(c *controller.Context, details *controller.ConnectionDetails) (controller.Status, bool, error) {
	if _, ok := c.Instance().Spec.Components[common.ComponentProxy]; !ok {
		return controller.Status{}, false, nil
	}

	pooler := &cnpgv1.Pooler{}
	if err := c.Get(pooler, poolerName(c.Name())); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.Provisioning("waiting for the CloudNativePG pooler"), true, nil
		}
		return controller.Status{}, true, fmt.Errorf("failed to get the CloudNativePG pooler: %w", err)
	}
	if pooler.Status.Phase == cnpgv1.PoolerPhaseFailed {
		message := string(pooler.Status.Phase)
		if pooler.Status.PhaseReason != "" {
			message += ": " + pooler.Status.PhaseReason
		}
		return controller.Failed(message), true, nil
	}
	if pooler.Status.Phase != cnpgv1.PoolerPhaseActive {
		message := "waiting for the CloudNativePG pooler"
		if pooler.Status.Phase != "" {
			message = fmt.Sprintf("%s (%s)", message, pooler.Status.Phase)
		}
		if pooler.Status.PhaseReason != "" {
			message += ": " + pooler.Status.PhaseReason
		}
		return controller.Provisioning(message), true, nil
	}

	want := int32(1)
	if pooler.Spec.Instances != nil {
		want = *pooler.Spec.Instances
	}
	deployment := &appsv1.Deployment{}
	if err := c.Get(deployment, pooler.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.Provisioning("waiting for the PgBouncer deployment"), true, nil
		}
		return controller.Status{}, true, fmt.Errorf("failed to get the PgBouncer deployment: %w", err)
	}
	if deployment.Status.ReadyReplicas < want {
		return controller.Provisioning(fmt.Sprintf("waiting for PgBouncer pods (%d/%d ready)",
			deployment.Status.ReadyReplicas, want)), true, nil
	}

	// listen_port is forced to PgBouncerPort. The credential Secret's port is
	// the PostgreSQL port, which is not where this Service listens.
	host := serviceHost(pooler.Name, c.Namespace())
	port := strconv.Itoa(pgbouncerconfig.PgBouncerPort)
	details.Host = host
	details.Port = port
	details.URI = postgresURI(details.Username, details.Password, host, port,
		details.AdditionalProperties["database"], details.AdditionalProperties["sslmode"])
	return controller.Status{}, false, nil
}

// instanceOfPoolerDeployment maps the PgBouncer Deployment the operator creates
// for a Pooler to its Instance, so a change in ready pods re-evaluates Status.
// The Deployment is owned by the Pooler, not the Instance, and its status can
// trail the pod events the runtime already watches.
func instanceOfPoolerDeployment(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels[utils.PodRoleLabelName] != string(utils.PodRolePooler) {
		return nil
	}
	cluster := labels[utils.ClusterLabelName]
	if cluster == "" || obj.GetName() != poolerName(cluster) {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: obj.GetNamespace(), Name: cluster}}}
}
