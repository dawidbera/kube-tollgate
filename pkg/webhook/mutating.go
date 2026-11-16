// Package webhook provides Mutating and Validating Admission Webhook handlers.
package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/metrics"
	"github.com/kube-tollgate/kube-tollgate/pkg/policy"
)

// MutatingHandler inspects incoming Pod admission reviews, injecting default resource allocations and FinOps metadata.
type MutatingHandler struct {
	Client       client.Client
	Decoder      admission.Decoder
	PolicyEngine policy.Engine
	Log          logr.Logger
}

// NewMutatingHandler constructs a new MutatingHandler with required dependencies.
func NewMutatingHandler(c client.Client, engine policy.Engine, scheme *runtime.Scheme, log logr.Logger) *MutatingHandler {
	decoder := admission.NewDecoder(scheme)
	return &MutatingHandler{
		Client:       c,
		Decoder:      decoder,
		PolicyEngine: engine,
		Log:          log,
	}
}

// Handle processes the admission review for Pod creation and updates, generating an RFC 6902 JSON patch.
func (h *MutatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	start := time.Now()
	decision := "allowed"

	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AdmissionDurationSeconds.WithLabelValues("mutating", string(req.Operation)).Observe(duration)
		metrics.AdmissionRequestsTotal.WithLabelValues("mutating", string(req.Operation), decision, req.Namespace).Inc()
	}()

	// Only evaluate Pod resources in mutating hook
	if req.Kind.Kind != "Pod" {
		return admission.Allowed("non-pod resource skipped")
	}

	if req.Operation == admissionv1.Delete {
		return admission.Allowed("delete operation skipped")
	}

	var pod corev1.Pod
	if err := h.Decoder.Decode(req, &pod); err != nil {
		decision = "error"
		h.Log.Error(err, "failed to decode pod from admission request")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Retrieve active policy for namespace
	pol, found := h.PolicyEngine.GetPolicy(req.Namespace)
	if !found || pol == nil {
		h.Log.V(1).Info("no TollgatePolicy found for namespace, skipping mutation", "namespace", req.Namespace)
		return admission.Allowed("no policy configured for namespace")
	}

	h.mutateLabels(ctx, req.Namespace, &pod, pol)
	h.mutateContainerResources(req.Namespace, &pod, pol)

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		decision = "error"
		h.Log.Error(err, "failed to serialize mutated pod")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// fetchParentNamespace attempts to read parent namespace metadata for label inheritance.
func (h *MutatingHandler) fetchParentNamespace(ctx context.Context, namespace string) *corev1.Namespace {
	if h.Client == nil || namespace == "" {
		return nil
	}
	var ns corev1.Namespace
	if err := h.Client.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		h.Log.V(1).Info("could not fetch parent namespace", "namespace", namespace, "error", err.Error())
		return nil
	}
	return &ns
}

// resolveLabelValue finds the appropriate value to inject from namespace annotations/labels or defaults.
func resolveLabelValue(key string, parentNS *corev1.Namespace, defaults map[string]string) string {
	if parentNS != nil {
		if val, ok := parentNS.Labels[key]; ok && val != "" {
			return val
		}
		if val, ok := parentNS.Annotations[key]; ok && val != "" {
			return val
		}
	}
	if defaults != nil {
		if val, ok := defaults[key]; ok && val != "" {
			return val
		}
	}
	return ""
}

// mutateLabels injects missing required FinOps labels from parent Namespace or policy defaults.
func (h *MutatingHandler) mutateLabels(ctx context.Context, namespace string, pod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) {
	if !pol.Spec.Labels.AutoInject.Enabled {
		return
	}

	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	var parentNS *corev1.Namespace
	if pol.Spec.Labels.AutoInject.InheritFromNamespace {
		parentNS = h.fetchParentNamespace(ctx, namespace)
	}

	for _, reqLabel := range pol.Spec.Labels.Required {
		if _, exists := pod.Labels[reqLabel.Key]; exists {
			continue
		}

		injectedVal := resolveLabelValue(reqLabel.Key, parentNS, pol.Spec.Labels.AutoInject.Defaults)
		if injectedVal != "" {
			pod.Labels[reqLabel.Key] = injectedVal
			metrics.MutationsAppliedTotal.WithLabelValues("injected_label", namespace).Inc()
			h.Log.V(1).Info("injected label onto pod", "key", reqLabel.Key, "value", injectedVal, "pod", pod.Name)
		}
	}
}

// injectMissingResource checks if quantity is empty and injects policy default if defined.
func injectMissingResource(target *corev1.ResourceList, resName corev1.ResourceName, currentVal, defaultVal resource.Quantity, metricType, namespace string) {
	if !defaultVal.IsZero() && currentVal.IsZero() {
		(*target)[resName] = defaultVal.DeepCopy()
		metrics.MutationsAppliedTotal.WithLabelValues(metricType, namespace).Inc()
	}
}

// applyContainerDefaults sets missing request and limit defaults on a single container.
func applyContainerDefaults(c *corev1.Container, defaults corev1.ResourceRequirements, namespace string) {
	if c.Resources.Requests == nil {
		c.Resources.Requests = make(corev1.ResourceList)
	}
	if c.Resources.Limits == nil {
		c.Resources.Limits = make(corev1.ResourceList)
	}

	if defaults.Requests != nil {
		injectMissingResource(&c.Resources.Requests, corev1.ResourceCPU, c.Resources.Requests[corev1.ResourceCPU], defaults.Requests[corev1.ResourceCPU], "injected_cpu_request", namespace)
		injectMissingResource(&c.Resources.Requests, corev1.ResourceMemory, c.Resources.Requests[corev1.ResourceMemory], defaults.Requests[corev1.ResourceMemory], "injected_memory_request", namespace)
	}

	if defaults.Limits != nil {
		injectMissingResource(&c.Resources.Limits, corev1.ResourceCPU, c.Resources.Limits[corev1.ResourceCPU], defaults.Limits[corev1.ResourceCPU], "injected_cpu_limit", namespace)
		injectMissingResource(&c.Resources.Limits, corev1.ResourceMemory, c.Resources.Limits[corev1.ResourceMemory], defaults.Limits[corev1.ResourceMemory], "injected_memory_limit", namespace)
	}
}

// mutateContainerResources injects default requests and limits into containers missing specifications.
func (h *MutatingHandler) mutateContainerResources(namespace string, pod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) {
	defaults := pol.Spec.Resources.Defaults
	for i := range pod.Spec.InitContainers {
		applyContainerDefaults(&pod.Spec.InitContainers[i], defaults, namespace)
	}
	for i := range pod.Spec.Containers {
		applyContainerDefaults(&pod.Spec.Containers[i], defaults, namespace)
	}
}
