package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/policy"
)

// setupTestEnvironment initializes common schemes, engine, and handler for mutating webhook tests.
func setupTestEnvironment(t *testing.T, policies []*tollgatev1alpha1.TollgatePolicy, namespaces []*corev1.Namespace) (*MutatingHandler, *policy.MemoryCache) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, tollgatev1alpha1.AddToScheme(scheme))

	clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
	for _, ns := range namespaces {
		clientBuilder = clientBuilder.WithObjects(ns)
	}
	fakeClient := clientBuilder.Build()

	cache := policy.NewMemoryCache()
	for _, p := range policies {
		cache.Set(p)
	}

	engine := policy.NewEngine(cache)
	handler := NewMutatingHandler(fakeClient, engine, scheme, logr.Discard())
	return handler, cache
}

// createAdmissionRequest creates a synthetic admission.Request containing a serialized Pod.
func createAdmissionRequest(t *testing.T, namespace string, pod *corev1.Pod) admission.Request {
	raw, err := json.Marshal(pod)
	require.NoError(t, err)

	return admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			UID:       "12345-uid",
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
			Namespace: namespace,
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Raw: raw,
			},
		},
	}
}

// TestMutatingHandler_NonPodResource ensures non-pod resources are skipped without mutation.
func TestMutatingHandler_NonPodResource(t *testing.T) {
	handler, _ := setupTestEnvironment(t, nil, nil)

	req := admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Kind:      metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			Namespace: "default",
			Operation: admissionv1.Create,
		},
	}

	resp := handler.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Empty(t, resp.Patches)
}

// TestMutatingHandler_NoPolicy ensures requests in namespaces without policies are allowed unmodified.
func TestMutatingHandler_NoPolicy(t *testing.T) {
	handler, _ := setupTestEnvironment(t, nil, nil)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "dev"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "web", Image: "nginx"}},
		},
	}
	req := createAdmissionRequest(t, "dev", pod)

	resp := handler.Handle(context.Background(), req)
	assert.True(t, resp.Allowed)
	assert.Empty(t, resp.Patches)
}

// TestMutatingHandler_ResourceDefaultsInjection tests injection of missing requests and limits.
func TestMutatingHandler_ResourceDefaultsInjection(t *testing.T) {
	p := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "res-policy", Namespace: "team-a"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Resources: tollgatev1alpha1.ResourcePolicy{
				Defaults: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			},
		},
	}

	handler, _ := setupTestEnvironment(t, []*tollgatev1alpha1.TollgatePolicy{p}, nil)

	// Scenario 1: Completely unbounded container
	unboundedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "unbounded", Namespace: "team-a"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "init", Image: "busybox"},
			},
			Containers: []corev1.Container{
				{Name: "app", Image: "nginx"},
			},
		},
	}
	req := createAdmissionRequest(t, "team-a", unboundedPod)
	resp := handler.Handle(context.Background(), req)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)

	// Verify patches contain expected resource additions
	patchBytes, err := json.Marshal(resp.Patches)
	require.NoError(t, err)
	patchStr := string(patchBytes)
	assert.Contains(t, patchStr, "100m")
	assert.Contains(t, patchStr, "128Mi")
	assert.Contains(t, patchStr, "500m")
	assert.Contains(t, patchStr, "512Mi")

	// Scenario 2: Partially bounded container (CPU request defined, memory and limits missing)
	partialPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "partial", Namespace: "team-a"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "nginx",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
					},
				},
			},
		},
	}
	reqPartial := createAdmissionRequest(t, "team-a", partialPod)
	respPartial := handler.Handle(context.Background(), reqPartial)

	assert.True(t, respPartial.Allowed)
	assert.NotEmpty(t, respPartial.Patches)
	patchPartialBytes, _ := json.Marshal(respPartial.Patches)
	patchPartialStr := string(patchPartialBytes)
	// Must inject memory request and limits, but NOT overwrite CPU request 250m
	assert.NotContains(t, patchPartialStr, `\"value\":\"100m\"`)
	assert.Contains(t, patchPartialStr, "128Mi")

	// Scenario 3: Fully bounded container (all requests and limits defined)
	boundedPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bounded", Namespace: "team-a"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "nginx",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
				},
			},
		},
	}
	reqBounded := createAdmissionRequest(t, "team-a", boundedPod)
	respBounded := handler.Handle(context.Background(), reqBounded)

	assert.True(t, respBounded.Allowed)
	assert.Empty(t, respBounded.Patches)
}

// TestMutatingHandler_LabelInheritance tests inheritance of required FinOps labels from Namespace annotations and defaults.
func TestMutatingHandler_LabelInheritance(t *testing.T) {
	p := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "label-policy", Namespace: "team-billing"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Labels: tollgatev1alpha1.LabelPolicy{
				Required: []tollgatev1alpha1.RequiredLabel{
					{Key: "cost-center", Pattern: "^CC-[0-9]{4}$"},
					{Key: "team"},
					{Key: "environment"},
				},
				AutoInject: tollgatev1alpha1.AutoInjectionConfig{
					Enabled:              true,
					InheritFromNamespace: true,
					Defaults: map[string]string{
						"cost-center": "CC-9999",
						"environment": "development",
					},
				},
			},
		},
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "team-billing",
			Labels: map[string]string{
				"cost-center": "CC-1234",
			},
			Annotations: map[string]string{
				"team": "billing-core",
			},
		},
	}

	handler, _ := setupTestEnvironment(t, []*tollgatev1alpha1.TollgatePolicy{p}, []*corev1.Namespace{ns})

	// Pod has no labels
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "team-billing"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: "alpine"}},
		},
	}
	req := createAdmissionRequest(t, "team-billing", pod)
	resp := handler.Handle(context.Background(), req)

	assert.True(t, resp.Allowed)
	assert.NotEmpty(t, resp.Patches)

	patchBytes, err := json.Marshal(resp.Patches)
	require.NoError(t, err)
	patchStr := string(patchBytes)

	// cost-center should be inherited from Namespace label "CC-1234"
	assert.Contains(t, patchStr, "CC-1234")
	// team should be inherited from Namespace annotation "billing-core"
	assert.Contains(t, patchStr, "billing-core")
	// environment was not on namespace, so fallback default "development" should be injected
	assert.Contains(t, patchStr, "development")

	// If pod already has a label, it should NOT be overwritten
	podWithLabel := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker-custom",
			Namespace: "team-billing",
			Labels: map[string]string{
				"cost-center": "CC-5555",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: "alpine"}},
		},
	}
	reqCustom := createAdmissionRequest(t, "team-billing", podWithLabel)
	respCustom := handler.Handle(context.Background(), reqCustom)

	assert.True(t, respCustom.Allowed)
	patchCustomBytes, _ := json.Marshal(respCustom.Patches)
	patchCustomStr := string(patchCustomBytes)
	// Should NOT replace CC-5555 with CC-1234
	assert.NotContains(t, patchCustomStr, "CC-1234")
}

// TestMutatingHandler_AutoInjectDisabled ensures no labels are injected when autoInject is disabled.
func TestMutatingHandler_AutoInjectDisabled(t *testing.T) {
	p := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "label-policy", Namespace: "team-billing"},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
			Labels: tollgatev1alpha1.LabelPolicy{
				Required: []tollgatev1alpha1.RequiredLabel{
					{Key: "cost-center"},
				},
				AutoInject: tollgatev1alpha1.AutoInjectionConfig{
					Enabled: false,
					Defaults: map[string]string{
						"cost-center": "CC-9999",
					},
				},
			},
		},
	}

	handler, _ := setupTestEnvironment(t, []*tollgatev1alpha1.TollgatePolicy{p}, nil)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "team-billing"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: "alpine"}},
		},
	}
	req := createAdmissionRequest(t, "team-billing", pod)
	resp := handler.Handle(context.Background(), req)

	assert.True(t, resp.Allowed)
	assert.Empty(t, resp.Patches)
}
