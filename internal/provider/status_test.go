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

func TestStatusMapsAdmissionValidationFailure(t *testing.T) {
	// CNPG SetAdmissionError writes PhaseDefinitionInvalid + PhaseReason.
	const detail = "spec.postgresql.synchronous.number must be lower than the number of instances"
	got, err := statusForCluster(t, cnpgv1.ClusterStatus{
		Phase:       cnpgv1.PhaseDefinitionInvalid,
		PhaseReason: detail,
		Conditions: []metav1.Condition{{
			Type:   string(cnpgv1.ConditionClusterReady),
			Status: metav1.ConditionFalse,
			Reason: string(cnpgv1.ClusterIsNotReady),
		}},
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.Phase != corev1alpha1.InstancePhaseFailed {
		t.Fatalf("phase = %q, want Failed", got.Phase)
	}
	want := cnpgv1.PhaseDefinitionInvalid + ": " + detail
	if got.Message != want {
		t.Fatalf("message = %q, want %q", got.Message, want)
	}
}

func TestStatusUsesInitializedCondition(t *testing.T) {
	tests := []struct {
		name    string
		status  cnpgv1.ClusterStatus
		want    corev1alpha1.InstancePhase
		message string
	}{
		{
			name: "Initialized=False reports Initializing with CNPG message",
			status: cnpgv1.ClusterStatus{
				Conditions: []metav1.Condition{{
					Type:    string(cnpgv1.ConditionInitialized),
					Status:  metav1.ConditionFalse,
					Reason:  string(cnpgv1.BootstrapPending),
					Message: "Cluster has not been bootstrapped yet",
				}},
			},
			want:    corev1alpha1.InstancePhaseInitializing,
			message: "Cluster has not been bootstrapped yet",
		},
		{
			name:    "missing Initialized condition and no generated node is Initializing",
			status:  cnpgv1.ClusterStatus{},
			want:    corev1alpha1.InstancePhaseInitializing,
			message: "waiting for CloudNativePG cluster to initialize",
		},
		{
			name: "Initialized=True with partial readiness is Provisioning",
			status: cnpgv1.ClusterStatus{
				Instances:      2,
				ReadyInstances: 1,
				Conditions: []metav1.Condition{{
					Type:   string(cnpgv1.ConditionInitialized),
					Status: metav1.ConditionTrue,
					Reason: string(cnpgv1.BootstrapCompleted),
				}},
			},
			want:    corev1alpha1.InstancePhaseProvisioning,
			message: "waiting for CloudNativePG cluster to be ready (1/2 instances ready)",
		},
		{
			name: "pre-1.30 upgrade fallback uses LatestGeneratedNode as initialized",
			status: cnpgv1.ClusterStatus{
				Instances:           2,
				ReadyInstances:      1,
				LatestGeneratedNode: 2,
			},
			want:    corev1alpha1.InstancePhaseProvisioning,
			message: "waiting for CloudNativePG cluster to be ready (1/2 instances ready)",
		},
		{
			name: "Initialized=True with no instances yet is still Provisioning",
			status: cnpgv1.ClusterStatus{
				Conditions: []metav1.Condition{{
					Type:   string(cnpgv1.ConditionInitialized),
					Status: metav1.ConditionTrue,
					Reason: string(cnpgv1.BootstrapCompleted),
				}},
			},
			want:    corev1alpha1.InstancePhaseProvisioning,
			message: "waiting for CloudNativePG cluster to be ready",
		},
		{
			name: "creating a replica while initialized stays Provisioning",
			status: cnpgv1.ClusterStatus{
				Phase:          cnpgv1.PhaseCreatingReplica,
				Instances:      2,
				ReadyInstances: 1,
				Conditions: []metav1.Condition{{
					Type:   string(cnpgv1.ConditionInitialized),
					Status: metav1.ConditionTrue,
				}},
			},
			want: corev1alpha1.InstancePhaseProvisioning,
		},
		{
			name: "supervised switchover while initialized stays Provisioning",
			status: cnpgv1.ClusterStatus{
				Phase:          cnpgv1.PhaseWaitingForUser,
				Instances:      2,
				ReadyInstances: 1,
				Conditions: []metav1.Condition{{
					Type:   string(cnpgv1.ConditionInitialized),
					Status: metav1.ConditionTrue,
				}},
			},
			want: corev1alpha1.InstancePhaseProvisioning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := statusForCluster(t, tt.status)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if got.Phase != tt.want {
				t.Fatalf("phase = %q, want %q (message: %s)", got.Phase, tt.want, got.Message)
			}
			if tt.message != "" && got.Message != tt.message {
				t.Fatalf("message = %q, want %q", got.Message, tt.message)
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
