package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/reconciler/hibernation"
	"github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

func suspendedInstance(suspended bool, proxy bool) *corev1alpha1.Instance {
	in := &corev1alpha1.Instance{
		ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace},
		Spec:       corev1alpha1.InstanceSpec{Suspend: &corev1alpha1.SuspendSpec{Suspended: suspended}},
	}
	if proxy {
		in.Spec.Components = map[string]corev1alpha1.ComponentSpec{common.ComponentProxy: {Replicas: ptr.To(int32(2))}}
	}
	return in
}

func suspendContext(t *testing.T, in *corev1alpha1.Instance, objs ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cnpgv1.AddToScheme, appsv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, in, common.ProviderName)
}

func TestApplySuspend(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *corev1alpha1.Instance
		want string
	}{
		{name: "suspended", in: suspendedInstance(true, false), want: "on"},
		{name: "resumed", in: suspendedInstance(false, false)},
		{name: "never suspended", in: &corev1alpha1.Instance{ObjectMeta: metav1.ObjectMeta{Name: "pg", Namespace: testNamespace}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pg := &cnpgv1.Cluster{}
			applySuspend(suspendContext(t, tt.in), pg)
			got, set := pg.Annotations[utils.HibernationAnnotationName]
			// Not rendering the annotation lets server-side apply remove it.
			if got != tt.want || set != (tt.want != "") {
				t.Fatalf("hibernation annotation = %q (set %v), want %q", got, set, tt.want)
			}
		})
	}
}

func TestBuildPoolerStopsWhileSuspended(t *testing.T) {
	for suspended, want := range map[bool]int32{true: 0, false: 2} {
		in := suspendedInstance(suspended, true)
		c := suspendContext(t, in)
		pooler := buildPooler(c, in.Spec.Components[common.ComponentProxy], mustProxyParams(t, c), false)
		if *pooler.Spec.Instances != want {
			t.Errorf("suspended %v: instances = %d, want %d", suspended, *pooler.Spec.Instances, want)
		}
	}
}

func hibernatingCluster(annotated bool, cond *metav1.Condition) *cnpgv1.Cluster {
	pg := readyCluster(&cnpgv1.Cluster{})
	if annotated {
		pg.Annotations = map[string]string{utils.HibernationAnnotationName: "on"}
	}
	if cond != nil {
		cond.Type = hibernation.HibernationConditionType
		pg.Status.Conditions = append(pg.Status.Conditions, *cond)
	}
	return pg
}

func TestStatusWhileSuspended(t *testing.T) {
	hibernated := &metav1.Condition{Status: metav1.ConditionTrue, Reason: hibernation.HibernationConditionReasonHibernated}
	for _, tt := range []struct {
		name        string
		proxy       bool
		cluster     *cnpgv1.Cluster
		objs        []client.Object
		wantPhase   corev1alpha1.InstancePhase
		wantMessage string
	}{
		{
			name:        "annotation not applied yet",
			cluster:     hibernatingCluster(false, nil),
			wantPhase:   corev1alpha1.InstancePhaseSuspending,
			wantMessage: "waiting for CloudNativePG to start hibernating the cluster",
		},
		{
			name:        "operator not reconciled yet",
			cluster:     hibernatingCluster(true, nil),
			wantPhase:   corev1alpha1.InstancePhaseSuspending,
			wantMessage: "waiting for CloudNativePG to start hibernating the cluster",
		},
		{
			name: "operator stopping pods",
			cluster: hibernatingCluster(true, &metav1.Condition{
				Status: metav1.ConditionFalse, Reason: hibernation.HibernationConditionReasonWaitingPodsDeletion, Message: "Waiting for pg-1 to be deleted",
			}),
			wantPhase:   corev1alpha1.InstancePhaseSuspending,
			wantMessage: "CloudNativePG: Waiting for pg-1 to be deleted",
		},
		{
			name:      "hibernated",
			cluster:   hibernatingCluster(true, hibernated),
			wantPhase: corev1alpha1.InstancePhaseSuspended,
		},
		{
			name:        "hibernated, PgBouncer still stopping",
			proxy:       true,
			cluster:     hibernatingCluster(true, hibernated),
			objs:        []client.Object{poolerDeployment(1)},
			wantPhase:   corev1alpha1.InstancePhaseSuspending,
			wantMessage: "waiting for PgBouncer pods to stop (1 left)",
		},
		{
			name:      "hibernated with PgBouncer stopped",
			proxy:     true,
			cluster:   hibernatingCluster(true, hibernated),
			objs:      []client.Object{poolerDeployment(0)},
			wantPhase: corev1alpha1.InstancePhaseSuspended,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.cluster.Name, tt.cluster.Namespace = "pg", testNamespace
			c := suspendContext(t, suspendedInstance(true, tt.proxy), append(tt.objs, tt.cluster)...)
			got, err := (&Provider{}).Status(c)
			if err != nil {
				t.Fatal(err)
			}
			if got.Phase != tt.wantPhase || got.Message != tt.wantMessage {
				t.Fatalf("status = %s %q, want %s %q", got.Phase, got.Message, tt.wantPhase, tt.wantMessage)
			}
		})
	}
}

func TestStatusSuspendedButFailed(t *testing.T) {
	pg := hibernatingCluster(true, nil)
	pg.Name, pg.Namespace = "pg", testNamespace
	pg.Status.Phase = cnpgv1.PhaseUnrecoverable
	got, err := (&Provider{}).Status(suspendContext(t, suspendedInstance(true, false), pg))
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != corev1alpha1.InstancePhaseFailed {
		t.Fatalf("phase = %s, want Failed", got.Phase)
	}
}
