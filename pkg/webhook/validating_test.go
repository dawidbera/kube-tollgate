package webhook

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/policy"
)

// setupValidatingTestEnv initializes the test environment with provided policies and pre-existing pods.
func setupValidatingTestEnv(t *testing.T, policies []*tollgatev1alpha1.TollgatePolicy, existingPods []*corev1.Pod) (*ValidatingHandler, *policy.MemoryCache) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, tollgatev1alpha1.AddToScheme(scheme))

	clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
	for _, p := range existingPods {
		clientBuilder = clientBuilder.WithObjects(p)
	}
	fakeClient := clientBuilder.Build()

	cache := policy.NewMemoryCache()
	for _, p := range policies {
		cache.Set(p)
	}

	engine := policy.NewEngine(cache)
	handler := NewValidatingHandler(fakeClient, engine, scheme, logr.Discard())
	return handler, cache
}

// TestValidatingHandler_LabelValidation verifies label presence and regex pattern matching.
func TestValidatingHandler_LabelValidation(t *testing.T) {
	pol := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "label-pol", Namespace: "billing"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Labels: tollgatev1alpha1.LabelPolicy{
				Required: []tollgatev1alpha1.RequiredLabel{
					{Key: "cost-center", Pattern: "^CC-[0-9]{4}$"},
					{Key: "environment", Pattern: "^(production|staging|development)$"},
					{Key: "team"},
				},
			},
		},
	}

	handler, _ := setupValidatingTestEnv(t, []*tollgatev1alpha1.TollgatePolicy{pol}, nil)

	// Scenario 1: Missing labels entirely
	podMissing := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "billing"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1", Image: "alpine"}},
		},
	}
	resp := handler.Handle(context.Background(), createAdmissionRequest(t, "billing", podMissing))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "MISSING_OR_INVALID_LABEL")
	assert.Contains(t, resp.Result.Message, "Missing required label 'cost-center'")

	// Scenario 2: Label present but fails pattern
	podInvalidPattern := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker",
			Namespace: "billing",
			Labels: map[string]string{
				"cost-center": "INVALID-123",
				"environment": "production",
				"team":        "core",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1", Image: "alpine"}},
		},
	}
	resp = handler.Handle(context.Background(), createAdmissionRequest(t, "billing", podInvalidPattern))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "does not match pattern '^CC-[0-9]{4}$'")

	// Scenario 3: All labels valid
	podValid := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker",
			Namespace: "billing",
			Labels: map[string]string{
				"cost-center": "CC-1234",
				"environment": "production",
				"team":        "core",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1", Image: "alpine"}},
		},
	}
	resp = handler.Handle(context.Background(), createAdmissionRequest(t, "billing", podValid))
	assert.True(t, resp.Allowed)
}

// TestValidatingHandler_ResourceBounds verifies enforcement of maxAllowedPerPod ceiling.
func TestValidatingHandler_ResourceBounds(t *testing.T) {
	pol := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "bounds-pol", Namespace: "dev"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Resources: tollgatev1alpha1.ResourcePolicy{
				MaxAllowedPerPod: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
	}

	handler, _ := setupValidatingTestEnv(t, []*tollgatev1alpha1.TollgatePolicy{pol}, nil)

	// Exceeds CPU bounds
	podOversized := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "huge-pod", Namespace: "dev"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "c1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}
	resp := handler.Handle(context.Background(), createAdmissionRequest(t, "dev", podOversized))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "EXCEEDS_MAX_BOUNDS")
	assert.Contains(t, resp.Result.Message, "Pod requested CPU (4000m) exceeds maxAllowedPerPod (2000m)")

	// Within bounds
	podSized := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "small-pod", Namespace: "dev"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "c1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}
	resp = handler.Handle(context.Background(), createAdmissionRequest(t, "dev", podSized))
	assert.True(t, resp.Allowed)
}

// TestValidatingHandler_MaxRatio verifies enforcement of limit-to-request ratios.
func TestValidatingHandler_MaxRatio(t *testing.T) {
	pol := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "ratio-pol", Namespace: "staging"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Resources: tollgatev1alpha1.ResourcePolicy{
				MaxRatio: tollgatev1alpha1.RatioConfig{
					CPU:    2.0,
					Memory: 2.0,
				},
			},
		},
	}

	handler, _ := setupValidatingTestEnv(t, []*tollgatev1alpha1.TollgatePolicy{pol}, nil)

	// CPU ratio 4.0 exceeds max 2.0
	podBadRatio := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-ratio", Namespace: "staging"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1000m"),
						},
					},
				},
			},
		},
	}
	resp := handler.Handle(context.Background(), createAdmissionRequest(t, "staging", podBadRatio))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "EXCEEDS_MAX_RATIO")
	assert.Contains(t, resp.Result.Message, "CPU limit/request ratio (4.00) exceeds maxRatio (2.00)")

	// Good ratio (requests 500m, limits 1000m -> ratio 2.0)
	podGoodRatio := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "good-ratio", Namespace: "staging"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("500m"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1000m"),
						},
					},
				},
			},
		},
	}
	resp = handler.Handle(context.Background(), createAdmissionRequest(t, "staging", podGoodRatio))
	assert.True(t, resp.Allowed)
}

// TestValidatingHandler_NamespaceBudget verifies aggregate budget checks and rejection message formatting.
func TestValidatingHandler_NamespaceBudget(t *testing.T) {
	pol := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default-budget-policy", Namespace: "team-billing"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Budget: tollgatev1alpha1.BudgetQuota{
				AggregateRequests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("64Gi"),
				},
			},
		},
	}

	// Active existing pod taking 30 cores
	existingActivePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: "team-billing"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "w1",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("30"),
						},
					},
				},
			},
		},
	}

	// Completed pod that should be ignored in budget calculation
	existingSucceededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-done", Namespace: "team-billing"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "wd",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("10"),
						},
					},
				},
			},
		},
	}

	handler, _ := setupValidatingTestEnv(t, []*tollgatev1alpha1.TollgatePolicy{pol}, []*corev1.Pod{existingActivePod, existingSucceededPod})

	// New pod requests 4 cores -> 30 + 4 = 34 cores > 32 ceiling
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-worker-7c98b8", Namespace: "team-billing"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("4"),
						},
					},
				},
			},
		},
	}

	resp := handler.Handle(context.Background(), createAdmissionRequest(t, "team-billing", newPod))
	assert.False(t, resp.Allowed)
	assert.Contains(t, resp.Result.Message, "BUDGET_EXCEEDED")
	assert.Contains(t, resp.Result.Message, "Current Namespace CPU Allocation: 30.00 cores")
	assert.Contains(t, resp.Result.Message, "Requested by Pod: 4.00 cores")
	assert.Contains(t, resp.Result.Message, "Budget Ceiling: 32.00 cores")
	assert.Contains(t, resp.Result.Message, "Exceeded by: 2.00 cores")
	assert.Contains(t, resp.Result.Message, "Downsize container requests in pod spec or request a quota increase in TollgatePolicy 'default-budget-policy'.")

	// Fitting pod requests 1 core -> 30 + 1 = 31 cores <= 32 ceiling
	fittingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "billing-small", Namespace: "team-billing"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "worker",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("1"),
						},
					},
				},
			},
		},
	}
	resp = handler.Handle(context.Background(), createAdmissionRequest(t, "team-billing", fittingPod))
	assert.True(t, resp.Allowed)
}

// TestValidatingHandler_AuditMode verifies that violations in Audit mode do not block admission.
func TestValidatingHandler_AuditMode(t *testing.T) {
	pol := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "audit-pol", Namespace: "audit-ns"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeAudit,
			Labels: tollgatev1alpha1.LabelPolicy{
				Required: []tollgatev1alpha1.RequiredLabel{
					{Key: "cost-center"},
				},
			},
		},
	}

	handler, _ := setupValidatingTestEnv(t, []*tollgatev1alpha1.TollgatePolicy{pol}, nil)

	// Missing label in Audit mode
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "audit-worker", Namespace: "audit-ns"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1", Image: "alpine"}},
		},
	}
	resp := handler.Handle(context.Background(), createAdmissionRequest(t, "audit-ns", pod))
	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Warnings)
	assert.Contains(t, resp.Warnings[0], "MISSING_OR_INVALID_LABEL")
}
