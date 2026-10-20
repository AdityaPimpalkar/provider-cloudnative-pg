package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStatusMapsTerminalClusterPhasesToFailed(t *testing.T) {
	const reason = "needs manual intervention"

	phases := []string{
		cnpgv1.PhaseUnrecoverable,
		cnpgv1.PhaseFailurePlugin,
		cnpgv1.PhaseUnknownPlugin,
		cnpgv1.PhaseCannotCreateClusterObjects,
		cnpgv1.PhaseImageCatalogError,
		cnpgv1.PhaseArchitectureBinaryMissing,
		cnpgv1.PhaseDefinitionInvalid,
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			got, err := statusForCluster(t, cnpgv1.ClusterStatus{
				Phase:          phase,
				PhaseReason:    reason,
				Instances:      2,
				ReadyInstances: 1,
			})
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if got.Phase != corev1alpha1.InstancePhaseFailed {
				t.Fatalf("phase = %q, want %q (message: %s)", got.Phase, corev1alpha1.InstancePhaseFailed, got.Message)
			}
			want := phase + ": " + reason
			if got.Message != want {
				t.Fatalf("message = %q, want %q", got.Message, want)
			}
		})
	}
}

func TestStatusOmitsEmptyPhaseReason(t *testing.T) {
	got, err := statusForCluster(t, cnpgv1.ClusterStatus{Phase: cnpgv1.PhaseUnrecoverable})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Phase != corev1alpha1.InstancePhaseFailed {
		t.Fatalf("phase = %q, want Failed", got.Phase)
	}
	if got.Message != cnpgv1.PhaseUnrecoverable {
		t.Fatalf("message = %q, want %q", got.Message, cnpgv1.PhaseUnrecoverable)
	}
}

func TestStatusLeavesInProgressPhasesAlone(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		want  corev1alpha1.InstancePhase
	}{
		{name: "creating a replica", phase: cnpgv1.PhaseCreatingReplica, want: corev1alpha1.InstancePhaseProvisioning},
		{name: "supervised switchover", phase: cnpgv1.PhaseWaitingForUser, want: corev1alpha1.InstancePhaseProvisioning},
		{name: "not initialized yet", want: corev1alpha1.InstancePhaseInitializing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := cnpgv1.ClusterStatus{Phase: tt.phase}
			if tt.want == corev1alpha1.InstancePhaseProvisioning {
				status.Instances = 2
				status.ReadyInstances = 1
			}
			got, err := statusForCluster(t, status)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if got.Phase != tt.want {
				t.Fatalf("phase = %q, want %q (message: %s)", got.Phase, tt.want, got.Message)
			}
		})
	}
}

func statusForCluster(t *testing.T, status cnpgv1.ClusterStatus) (controller.Status, error) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace}}
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: instance.Name, Namespace: instance.Namespace},
		Status:     status,
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).Build()
	c := controller.NewContext(context.Background(), cl, instance, "provider-cloudnative-pg")
	return (&Provider{}).Status(c)
}
