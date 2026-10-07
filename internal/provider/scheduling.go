package provider

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

// applyScheduling places the cluster's instance pods. Unless the user brings
// their own affinity, the operator's preferred anti-affinity on the node
// hostname spreads them; an affinity, even {}, replaces it.
func applyScheduling(pg *cnpgv1.Cluster, policy *commonv1alpha1.SchedulingPolicy) {
	if policy == nil {
		return
	}
	pg.Spec.SchedulerName = policy.SchedulerName
	if len(policy.NodeSelector) > 0 {
		pg.Spec.Affinity.NodeSelector = policy.NodeSelector
	}
	if len(policy.Tolerations) > 0 {
		pg.Spec.Affinity.Tolerations = policy.Tolerations
	}
	if policy.Affinity != nil {
		pg.Spec.Affinity.EnablePodAntiAffinity = new(false)
		pg.Spec.Affinity.NodeAffinity = policy.Affinity.NodeAffinity
		pg.Spec.Affinity.AdditionalPodAffinity = policy.Affinity.PodAffinity
		pg.Spec.Affinity.AdditionalPodAntiAffinity = policy.Affinity.PodAntiAffinity
	}
	pg.Spec.TopologySpreadConstraints = controller.TopologySpreadConstraints(policy, map[string]string{
		utils.ClusterLabelName: pg.Name,
		utils.PodRoleLabelName: string(utils.PodRoleInstance),
	})
}
