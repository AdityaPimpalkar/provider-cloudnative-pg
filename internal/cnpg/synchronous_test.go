package cnpg

import (
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"

	"github.com/adityapimpalkar/provider-cloudnative-pg/definition/components"
)

func TestValidateSynchronous(t *testing.T) {
	withSync := func(sync *cnpgv1.SynchronousReplicaConfiguration) *components.CNPGCustomSpec {
		return &components.CNPGCustomSpec{PostgresConfiguration: &cnpgv1.PostgresConfiguration{Synchronous: sync}}
	}
	tests := []struct {
		name     string
		replicas int32
		custom   *components.CNPGCustomSpec
		wantErr  bool
	}{
		{name: "asynchronous", replicas: 1, custom: &components.CNPGCustomSpec{}},
		{name: "one of three", replicas: 3, custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "any", Number: 1})},
		{name: "two of three", replicas: 3, custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "first", Number: 2, DataDurability: "preferred"})},
		{name: "as many as replicas", replicas: 3, custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "any", Number: 3}), wantErr: true},
		{name: "single node", replicas: 1, custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "any", Number: 1}), wantErr: true},
		{name: "zero", replicas: 3, custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "any"}), wantErr: true},
		{
			name: "external standby names count", replicas: 1,
			custom: withSync(&cnpgv1.SynchronousReplicaConfiguration{Method: "first", Number: 1, StandbyNamesPost: []string{"dr"}}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := corev1alpha1.ComponentSpec{Replicas: pointer.To(tt.replicas)}
			if err := ValidateSynchronous(engine, tt.custom); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
