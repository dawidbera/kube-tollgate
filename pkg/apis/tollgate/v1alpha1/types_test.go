package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// TestTollgatePolicyYAMLDeserialization verifies that valid policy YAML can be parsed into TollgatePolicy struct.
func TestTollgatePolicyYAMLDeserialization(t *testing.T) {
	yamlData := `
apiVersion: finops.tollgate.io/v1alpha1
kind: TollgatePolicy
metadata:
  name: default-budget-policy
  namespace: team-billing
spec:
  mode: Enforce
  labels:
    required:
      - key: "cost-center"
        pattern: "^CC-[0-9]{4}$"
      - key: "team"
      - key: "environment"
        pattern: "^(production|staging|development)$"
    autoInject:
      enabled: true
      inheritFromNamespace: true
      defaults:
        cost-center: "CC-9999"
        environment: "development"
  resources:
    defaults:
      requests:
        cpu: "100m"
        memory: "128Mi"
      limits:
        cpu: "500m"
        memory: "512Mi"
    maxAllowedPerPod:
      cpu: "4"
      memory: "8Gi"
    maxRatio:
      cpu: 4.0
      memory: 2.0
  budget:
    aggregateRequests:
      cpu: "32"
      memory: "64Gi"
`

	var policy TollgatePolicy
	err := yaml.Unmarshal([]byte(yamlData), &policy)
	require.NoError(t, err)

	assert.Equal(t, "default-budget-policy", policy.Name)
	assert.Equal(t, "team-billing", policy.Namespace)
	assert.Equal(t, PolicyModeEnforce, policy.Spec.Mode)

	// Verify labels
	require.Len(t, policy.Spec.Labels.Required, 3)
	assert.Equal(t, "cost-center", policy.Spec.Labels.Required[0].Key)
	assert.Equal(t, "^CC-[0-9]{4}$", policy.Spec.Labels.Required[0].Pattern)
	assert.True(t, policy.Spec.Labels.AutoInject.Enabled)
	assert.True(t, policy.Spec.Labels.AutoInject.InheritFromNamespace)
	assert.Equal(t, "CC-9999", policy.Spec.Labels.AutoInject.Defaults["cost-center"])

	// Verify resources defaults
	reqCPU := policy.Spec.Resources.Defaults.Requests[corev1.ResourceCPU]
	assert.Equal(t, resource.MustParse("100m"), reqCPU)
	limitMem := policy.Spec.Resources.Defaults.Limits[corev1.ResourceMemory]
	assert.Equal(t, resource.MustParse("512Mi"), limitMem)

	// Verify max allowed
	maxCPU := policy.Spec.Resources.MaxAllowedPerPod[corev1.ResourceCPU]
	assert.Equal(t, resource.MustParse("4"), maxCPU)

	// Verify ratio
	assert.Equal(t, 4.0, policy.Spec.Resources.MaxRatio.CPU)
	assert.Equal(t, 2.0, policy.Spec.Resources.MaxRatio.Memory)

	// Verify budget
	budgetCPU := policy.Spec.Budget.AggregateRequests[corev1.ResourceCPU]
	assert.Equal(t, resource.MustParse("32"), budgetCPU)
	budgetMem := policy.Spec.Budget.AggregateRequests[corev1.ResourceMemory]
	assert.Equal(t, resource.MustParse("64Gi"), budgetMem)
}

// TestTollgatePolicyDeepCopy verifies that DeepCopy creates an isolated clone of the policy.
func TestTollgatePolicyDeepCopy(t *testing.T) {
	original := &TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "dev",
		},
		Spec: TollgatePolicySpec{
			Mode: PolicyModeEnforce,
			Labels: LabelPolicy{
				Required: []RequiredLabel{
					{Key: "env", Pattern: "^dev$"},
				},
				AutoInject: AutoInjectionConfig{
					Defaults: map[string]string{"env": "dev"},
				},
			},
		},
	}

	copied := original.DeepCopy()
	require.NotNil(t, copied)
	assert.Equal(t, original.Name, copied.Name)

	// Modify copy and assert original unchanged
	copied.Spec.Labels.AutoInject.Defaults["env"] = "prod"
	assert.Equal(t, "dev", original.Spec.Labels.AutoInject.Defaults["env"])
	assert.Equal(t, "prod", copied.Spec.Labels.AutoInject.Defaults["env"])
}
