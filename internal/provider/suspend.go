package provider

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/reconciler/hibernation"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// applySuspend suspends the cluster with CloudNativePG's declarative
// hibernation: the operator stops every instance and keeps their PVCs.
//
// The annotation is rendered only while a suspend is requested. On resume the
// provider stops applying it, so server-side apply removes it and the
// operator starts the instances again. Not rendering "off" leaves a
// hibernation someone else set on the Cluster alone.
func applySuspend(c *controller.Context, pg *cnpgv1.Cluster) {
	if !c.Instance().SuspendRequested() {
		return
	}
	if pg.Annotations == nil {
		pg.Annotations = map[string]string{}
	}
	pg.Annotations[utils.HibernationAnnotationName] = string(utils.HibernationAnnotationValueOn)
}

// suspendStatus reports a cluster asked to suspend: Suspended once the
// operator has stopped every instance and the PgBouncer pods are gone,
// Suspending with the operator's progress until then.
func suspendStatus(c *controller.Context, pg *cnpgv1.Cluster) (controller.Status, error) {
	cond := meta.FindStatusCondition(pg.Status.Conditions, hibernation.HibernationConditionType)
	switch {
	case pg.Annotations[utils.HibernationAnnotationName] != string(utils.HibernationAnnotationValueOn) || cond == nil:
		return controller.Suspending("waiting for CloudNativePG to start hibernating the cluster"), nil
	case cond.Status != metav1.ConditionTrue:
		message := cond.Message
		if message == "" {
			message = cond.Reason
		}
		return controller.Suspending("CloudNativePG: " + message), nil
	}

	if _, ok := c.Instance().Spec.Components[common.ComponentProxy]; ok {
		deployment := &appsv1.Deployment{}
		err := c.Get(deployment, poolerName(c.Name()))
		if err != nil && !apierrors.IsNotFound(err) {
			return controller.Status{}, fmt.Errorf("failed to get the PgBouncer deployment: %w", err)
		}
		if err == nil && deployment.Status.Replicas > 0 {
			return controller.Suspending(fmt.Sprintf("waiting for PgBouncer pods to stop (%d left)", deployment.Status.Replicas)), nil
		}
	}
	return controller.Suspended(), nil
}
