package barman

import (
	"testing"

	"github.com/AlekSi/pointer"
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

func TestArchiveObjectStoreName(t *testing.T) {
	barmanPlugin := func(enabled *bool) cnpgv1.PluginConfiguration {
		return cnpgv1.PluginConfiguration{
			Name:       PluginName,
			Enabled:    enabled,
			Parameters: map[string]string{PluginParameterObjectStore: "s3"},
		}
	}

	tests := []struct {
		name    string
		plugins []cnpgv1.PluginConfiguration
		want    string
	}{
		{name: "enabled plugin", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(pointer.To(true))}, want: "s3"},
		{name: "plugin enabled by default", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(nil)}, want: "s3"},
		{name: "disabled plugin", plugins: []cnpgv1.PluginConfiguration{barmanPlugin(pointer.To(false))}},
		{name: "no plugins"},
		{
			name: "only other plugins",
			plugins: []cnpgv1.PluginConfiguration{{
				Name:       "other.example.com",
				Parameters: map[string]string{PluginParameterObjectStore: "s3"},
			}},
		},
		{
			name: "barman plugin after another plugin",
			plugins: []cnpgv1.PluginConfiguration{
				{Name: "other.example.com"},
				barmanPlugin(pointer.To(true)),
			},
			want: "s3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Plugins: tt.plugins}}
			if got := ArchiveObjectStoreName(cluster); got != tt.want {
				t.Fatalf("ArchiveObjectStoreName() = %q, want %q", got, tt.want)
			}
		})
	}
}
