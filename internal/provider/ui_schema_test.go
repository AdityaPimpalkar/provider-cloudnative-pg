package provider

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/cnpg"
	"github.com/adityapimpalkar/provider-cloudnative-pg/internal/common"
)

// uiFields returns every component of the generated UI schemas that writes a
// path.
func uiFields(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(providerSpecPath)
	require.NoError(t, err)
	var provider struct {
		Spec struct {
			UISchema map[string]any `json:"uiSchema"`
		} `json:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(data, &provider))
	require.NotEmpty(t, provider.Spec.UISchema)

	var fields []map[string]any
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if _, ok := n["path"].(string); ok {
				fields = append(fields, n)
			}
			for _, v := range n {
				walk(v)
			}
		case []any:
			for _, v := range n {
				walk(v)
			}
		}
	}
	walk(provider.Spec.UISchema)
	return fields
}

// resourceField matches the paths of CPU, memory and disk quantities: the
// resources of a component, its storage, and the WAL disk.
var resourceField = regexp.MustCompile(
	`^spec\.components\.(\w+)\.(?:resources\.(?:limits|requests)\.(cpu|memory)|storage\.(size)|parameters\.walStorage\.(size))$`)

// wantMemoryUnit is the unit of each component's memory fields: PgBouncer
// needs little memory, and a fraction of a Gi is not a whole number of bytes.
var wantMemoryUnit = map[string]string{
	common.ComponentEngine: "Gi",
	common.ComponentProxy:  "Mi",
}

func quantity(t *testing.T, path string, v any, unit string) resource.Quantity {
	t.Helper()
	n, ok := v.(float64)
	require.True(t, ok, "%s: %v is not a number", path, v)
	q, err := resource.ParseQuantity(strconv.FormatFloat(n, 'f', -1, 64) + unit)
	require.NoError(t, err, "%s writes %v%s", path, n, unit)
	return q
}

// Resource fields are quantity fields: the form holds a number in the field's
// unit and writes a quantity string, so a fractional CPU ("0.5") reaches the
// API as a string it accepts. CPU is in cores (no unit); memory and disk carry
// their unit. The smallest value and the default a component's fields offer
// must pass the provider's own resource validation.
func TestUIResourceFieldsAreQuantities(t *testing.T) {
	t.Parallel()
	minimums := map[string]*corev1.ResourceRequirements{}
	defaults := map[string]*corev1.ResourceRequirements{}
	requirements := func(set map[string]*corev1.ResourceRequirements, component string) *corev1.ResourceRequirements {
		if set[component] == nil {
			set[component] = &corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
		}
		return set[component]
	}

	var checked int
	for _, field := range uiFields(t) {
		path := field["path"].(string)
		m := resourceField.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		checked++
		component, kind := m[1], m[2]+m[3]+m[4]

		require.Equal(t, "quantity", field["uiType"], path)
		params, _ := field["fieldParams"].(map[string]any)
		validation, _ := field["validation"].(map[string]any)
		assert.NotContains(t, params, "badge", path)
		assert.NotContains(t, params, "badgeToApi", path)

		unit, _ := params["unit"].(string)
		switch kind {
		case "cpu":
			assert.Empty(t, unit, "%s is in cores", path)
			assert.Equal(t, "CPU", params["unitLabel"], path)
			assert.InDelta(t, 0.001, validation["multipleOf"], 0, "%s takes millicore precision, as Validate does", path)
		case "memory":
			assert.Equal(t, wantMemoryUnit[component], unit, path)
		default:
			assert.Equal(t, "Gi", unit, path)
			assert.Equal(t, true, validation["int"], "%s takes whole Gi", path)
		}

		minimum := quantity(t, path, validation["min"], unit)
		assert.Positive(t, minimum.Sign(), "%s min", path)
		var def *resource.Quantity
		if v, ok := params["defaultValue"]; ok {
			q := quantity(t, path, v, unit)
			def = &q
			assert.GreaterOrEqual(t, q.Cmp(minimum), 0, "%s default %s is below its min %s", path, q.String(), minimum.String())
		}

		if kind == "cpu" || kind == "memory" {
			// Requests and limits share their bounds, so one value stands for both.
			name := corev1.ResourceName(kind)
			mins := requirements(minimums, component)
			mins.Requests[name], mins.Limits[name] = minimum, minimum
			if def != nil {
				defs := requirements(defaults, component)
				defs.Requests[name], defs.Limits[name] = *def, *def
			}
		}
	}
	require.Positive(t, checked)

	for name, set := range map[string]map[string]*corev1.ResourceRequirements{"min": minimums, "defaultValue": defaults} {
		for _, component := range []string{common.ComponentEngine, common.ComponentProxy} {
			require.Contains(t, set, component, "%s resources of %s", name, component)
			assert.NoError(t, cnpg.ValidateResources(set[component]), "the %s resources the UI offers for %s", name, component)
		}
	}
}
