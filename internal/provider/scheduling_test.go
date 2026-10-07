package provider

import (
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
)

func TestApplyScheduling(t *testing.T) {
	nodeAffinity := &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
				Key: "disktype", Operator: corev1.NodeSelectorOpIn, Values: []string{"ssd"},
			}}}},
		},
	}
	antiAffinity := &corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{TopologyKey: corev1.LabelTopologyZone}},
	}
	tolerations := []corev1.Toleration{{Key: "db", Operator: corev1.TolerationOpExists}}
	customAffinity := cnpgv1.AffinityConfiguration{PodAntiAffinityType: cnpgv1.PodAntiAffinityTypeRequired}
	instancePods := &metav1.LabelSelector{MatchLabels: map[string]string{
		"cnpg.io/cluster": "pg",
		"cnpg.io/podRole": "instance",
	}}
	ownSelector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "other"}}

	tests := []struct {
		name     string
		affinity cnpgv1.AffinityConfiguration
		policy   *commonv1alpha1.SchedulingPolicy
		wantSpec cnpgv1.ClusterSpec
	}{
		{
			name:     "no policy keeps the operator default",
			wantSpec: cnpgv1.ClusterSpec{},
		},
		{
			name:     "no policy keeps parameters.affinity",
			affinity: customAffinity,
			wantSpec: cnpgv1.ClusterSpec{Affinity: customAffinity},
		},
		{
			name:     "empty affinity disables the operator anti-affinity",
			policy:   &commonv1alpha1.SchedulingPolicy{Affinity: &corev1.Affinity{}},
			wantSpec: cnpgv1.ClusterSpec{Affinity: cnpgv1.AffinityConfiguration{EnablePodAntiAffinity: new(false)}},
		},
		{
			name: "affinity replaces the operator anti-affinity",
			policy: &commonv1alpha1.SchedulingPolicy{Affinity: &corev1.Affinity{
				NodeAffinity:    nodeAffinity,
				PodAntiAffinity: antiAffinity,
			}},
			wantSpec: cnpgv1.ClusterSpec{Affinity: cnpgv1.AffinityConfiguration{
				EnablePodAntiAffinity:     new(false),
				NodeAffinity:              nodeAffinity,
				AdditionalPodAntiAffinity: antiAffinity,
			}},
		},
		{
			name: "node selector, tolerations and scheduler keep the operator anti-affinity",
			policy: &commonv1alpha1.SchedulingPolicy{
				SchedulerName: "custom",
				NodeSelector:  map[string]string{"pool": "db"},
				Tolerations:   tolerations,
			},
			wantSpec: cnpgv1.ClusterSpec{
				SchedulerName: "custom",
				Affinity: cnpgv1.AffinityConfiguration{
					NodeSelector: map[string]string{"pool": "db"},
					Tolerations:  tolerations,
				},
			},
		},
		{
			name:     "spread constraints keep parameters.affinity and select instance pods by default",
			affinity: customAffinity,
			policy: &commonv1alpha1.SchedulingPolicy{TopologySpreadConstraints: &[]corev1.TopologySpreadConstraint{
				{MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone},
				{MaxSkew: 1, TopologyKey: corev1.LabelHostname, LabelSelector: ownSelector},
			}},
			wantSpec: cnpgv1.ClusterSpec{
				Affinity: customAffinity,
				TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
					{MaxSkew: 1, TopologyKey: corev1.LabelTopologyZone, LabelSelector: instancePods},
					{MaxSkew: 1, TopologyKey: corev1.LabelHostname, LabelSelector: ownSelector},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pg := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "pg"},
				Spec:       cnpgv1.ClusterSpec{Affinity: tt.affinity},
			}
			applyScheduling(pg, tt.policy)
			if !reflect.DeepEqual(pg.Spec, tt.wantSpec) {
				t.Errorf("spec = %+v, want %+v", pg.Spec, tt.wantSpec)
			}
		})
	}
}
