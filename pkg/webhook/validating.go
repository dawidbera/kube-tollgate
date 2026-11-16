// Package webhook provides Mutating and Validating Admission Webhook handlers.
package webhook

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/metrics"
	"github.com/kube-tollgate/kube-tollgate/pkg/policy"
)

// ValidatingHandler inspects incoming Pod admission reviews, validating FinOps labels, resource bounds, ratios, and namespace budgets.
type ValidatingHandler struct {
	Client       client.Client
	Decoder      admission.Decoder
	PolicyEngine policy.Engine
	Log          logr.Logger
}

// NewValidatingHandler constructs a new ValidatingHandler with required dependencies.
func NewValidatingHandler(c client.Client, engine policy.Engine, scheme *runtime.Scheme, log logr.Logger) *ValidatingHandler {
	decoder := admission.NewDecoder(scheme)
	return &ValidatingHandler{
		Client:       c,
		Decoder:      decoder,
		PolicyEngine: engine,
		Log:          log,
	}
}

// Handle processes the admission review for Pod validation.
func (h *ValidatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	start := time.Now()
	decision := "allowed"

	defer func() {
		duration := time.Since(start).Seconds()
		metrics.AdmissionDurationSeconds.WithLabelValues("validating", string(req.Operation)).Observe(duration)
		metrics.AdmissionRequestsTotal.WithLabelValues("validating", string(req.Operation), decision, req.Namespace).Inc()
	}()

	// Only evaluate Pod resources in validating hook
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

	pol, found := h.PolicyEngine.GetPolicy(req.Namespace)
	if !found || pol == nil {
		return admission.Allowed("no policy configured for namespace")
	}

	// 1. Validate Required Labels & Patterns
	if labelViolations := validateLabels(&pod, pol); len(labelViolations) > 0 {
		return h.handleViolations(req, "MISSING_OR_INVALID_LABEL", labelViolations, pol,
			"Add all mandatory FinOps labels matching their required regex patterns.", &decision)
	}

	// 2. Validate Resource Bounds & Max Ratios
	if boundsViolations := validateResourceBounds(&pod, pol); len(boundsViolations) > 0 {
		return h.handleViolations(req, "EXCEEDS_MAX_BOUNDS", boundsViolations, pol,
			"Reduce container resource requests/limits below policy maxAllowedPerPod ceiling.", &decision)
	}

	if ratioViolations := validateLimitToRequestRatios(&pod, pol); len(ratioViolations) > 0 {
		return h.handleViolations(req, "EXCEEDS_MAX_RATIO", ratioViolations, pol,
			"Reduce limit-to-request ratio in container resource specifications.", &decision)
	}

	// 3. Namespace Aggregate Budget Quota Check
	budgetDetails, exceeded, bReason := h.validateNamespaceBudget(ctx, req.Namespace, pod.Name, &pod, pol)
	if exceeded {
		return h.handleViolations(req, bReason, budgetDetails, pol,
			fmt.Sprintf("Downsize container requests in pod spec or request a quota increase in TollgatePolicy '%s'.", pol.Name), &decision)
	}

	return admission.Allowed("workload satisfies FinOps tollgate policies")
}

// handleViolations formats the denial or audit warning based on policy enforcement mode.
func (h *ValidatingHandler) handleViolations(req admission.Request, reason string, details []string, pol *tollgatev1alpha1.TollgatePolicy, action string, decision *string) admission.Response {
	resourceName := fmt.Sprintf("Pod/%s", req.Name)
	if req.Name == "" {
		resourceName = "Pod/" + string(req.UID)
	}

	msg := formatRejectionMessage(req.Namespace, resourceName, reason, details, action)

	if pol.Spec.Mode == tollgatev1alpha1.PolicyModeAudit {
		h.Log.Info("audit violation detected, admitting workload", "namespace", req.Namespace, "reason", reason)
		resp := admission.Allowed("admitted with FinOps policy warnings")
		return resp.WithWarnings(msg)
	}

	*decision = "denied"
	metrics.RejectionsTotal.WithLabelValues(reason, req.Namespace, "Pod").Inc()
	h.Log.Info("admission denied by policy", "namespace", req.Namespace, "reason", reason)
	return admission.Denied(msg)
}

// validateLabels checks that all required labels are present on the Pod and conform to regex patterns.
func validateLabels(pod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) []string {
	var violations []string
	labels := pod.Labels

	for _, reqLabel := range pol.Spec.Labels.Required {
		val, exists := labels[reqLabel.Key]
		if !exists || strings.TrimSpace(val) == "" {
			violations = append(violations, fmt.Sprintf("Missing required label '%s'", reqLabel.Key))
			continue
		}

		if reqLabel.Pattern != "" {
			matched, err := regexp.MatchString(reqLabel.Pattern, val)
			if err != nil || !matched {
				violations = append(violations, fmt.Sprintf("Label '%s' value '%s' does not match pattern '%s'", reqLabel.Key, val, reqLabel.Pattern))
			}
		}
	}
	return violations
}

// validateResourceBounds verifies that Pod effective resource requests do not exceed maxAllowedPerPod.
func validateResourceBounds(pod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) []string {
	maxAllowed := pol.Spec.Resources.MaxAllowedPerPod
	if maxAllowed == nil {
		return nil
	}

	var violations []string
	podCPU, podMem := calculatePodRequests(pod)

	if maxCPU, ok := maxAllowed[corev1.ResourceCPU]; ok && !maxCPU.IsZero() {
		if podCPU > maxCPU.MilliValue() {
			violations = append(violations, fmt.Sprintf("Pod requested CPU (%dm) exceeds maxAllowedPerPod (%dm)", podCPU, maxCPU.MilliValue()))
		}
	}

	if maxMem, ok := maxAllowed[corev1.ResourceMemory]; ok && !maxMem.IsZero() {
		if podMem > maxMem.Value() {
			violations = append(violations, fmt.Sprintf("Pod requested Memory (%d bytes) exceeds maxAllowedPerPod (%d bytes)", podMem, maxMem.Value()))
		}
	}

	return violations
}

// checkContainerRatio calculates ratio of limit to request for a container and validates against policy.
func checkContainerRatio(c *corev1.Container, maxRatio tollgatev1alpha1.RatioConfig) []string {
	var violations []string
	if maxRatio.CPU > 0 && c.Resources.Requests != nil && c.Resources.Limits != nil {
		reqCPU := c.Resources.Requests.Cpu().MilliValue()
		limitCPU := c.Resources.Limits.Cpu().MilliValue()
		if reqCPU > 0 && limitCPU > 0 {
			ratio := float64(limitCPU) / float64(reqCPU)
			if ratio > maxRatio.CPU+1e-6 {
				violations = append(violations, fmt.Sprintf("Container '%s' CPU limit/request ratio (%.2f) exceeds maxRatio (%.2f)", c.Name, ratio, maxRatio.CPU))
			}
		}
	}

	if maxRatio.Memory > 0 && c.Resources.Requests != nil && c.Resources.Limits != nil {
		reqMem := c.Resources.Requests.Memory().Value()
		limitMem := c.Resources.Limits.Memory().Value()
		if reqMem > 0 && limitMem > 0 {
			ratio := float64(limitMem) / float64(reqMem)
			if ratio > maxRatio.Memory+1e-6 {
				violations = append(violations, fmt.Sprintf("Container '%s' Memory limit/request ratio (%.2f) exceeds maxRatio (%.2f)", c.Name, ratio, maxRatio.Memory))
			}
		}
	}

	return violations
}

// validateLimitToRequestRatios verifies that container limit/request ratios do not exceed maxRatio.
func validateLimitToRequestRatios(pod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) []string {
	maxRatio := pol.Spec.Resources.MaxRatio
	if maxRatio.CPU <= 0 && maxRatio.Memory <= 0 {
		return nil
	}

	var violations []string
	for i := range pod.Spec.InitContainers {
		violations = append(violations, checkContainerRatio(&pod.Spec.InitContainers[i], maxRatio)...)
	}
	for i := range pod.Spec.Containers {
		violations = append(violations, checkContainerRatio(&pod.Spec.Containers[i], maxRatio)...)
	}

	return violations
}

// calculatePodRequests computes the effective aggregate CPU millicores and memory bytes for a pod.
func calculatePodRequests(pod *corev1.Pod) (int64, int64) {
	var regularCPU, regularMem int64
	for _, c := range pod.Spec.Containers {
		if c.Resources.Requests != nil {
			regularCPU += c.Resources.Requests.Cpu().MilliValue()
			regularMem += c.Resources.Requests.Memory().Value()
		}
	}

	var maxInitCPU, maxInitMem int64
	for _, c := range pod.Spec.InitContainers {
		if c.Resources.Requests != nil {
			if initCPU := c.Resources.Requests.Cpu().MilliValue(); initCPU > maxInitCPU {
				maxInitCPU = initCPU
			}
			if initMem := c.Resources.Requests.Memory().Value(); initMem > maxInitMem {
				maxInitMem = initMem
			}
		}
	}

	effectiveCPU := regularCPU
	if maxInitCPU > effectiveCPU {
		effectiveCPU = maxInitCPU
	}

	effectiveMem := regularMem
	if maxInitMem > effectiveMem {
		effectiveMem = maxInitMem
	}

	return effectiveCPU, effectiveMem
}

// sumActivePods aggregates CPU and memory requests across all active pods in a namespace.
func sumActivePods(items []corev1.Pod, currentPodName string) (int64, int64) {
	var currentCPU, currentMem int64
	for _, p := range items {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if p.Name == currentPodName && currentPodName != "" {
			continue
		}
		pCPU, pMem := calculatePodRequests(&p)
		currentCPU += pCPU
		currentMem += pMem
	}
	return currentCPU, currentMem
}

// evaluateCPUBudget checks if combined CPU requests exceed namespace ceiling and updates telemetry.
func evaluateCPUBudget(currentCPU, proposedCPU, ceilCPUMilli int64, namespace string) ([]string, bool) {
	totalCPU := currentCPU + proposedCPU
	ratio := float64(totalCPU) / float64(ceilCPUMilli)
	metrics.NamespaceBudgetRequestsRatio.WithLabelValues(namespace, "cpu").Set(ratio)

	if totalCPU > ceilCPUMilli {
		return []string{
			fmt.Sprintf("Current Namespace CPU Allocation: %.2f cores", float64(currentCPU)/1000.0),
			fmt.Sprintf("Requested by Pod: %.2f cores", float64(proposedCPU)/1000.0),
			fmt.Sprintf("Budget Ceiling: %.2f cores", float64(ceilCPUMilli)/1000.0),
			fmt.Sprintf("Exceeded by: %.2f cores", float64(totalCPU-ceilCPUMilli)/1000.0),
		}, true
	}
	return nil, false
}

// evaluateMemoryBudget checks if combined Memory requests exceed namespace ceiling and updates telemetry.
func evaluateMemoryBudget(currentMem, proposedMem, ceilMemBytes int64, namespace string) ([]string, bool) {
	totalMem := currentMem + proposedMem
	ratio := float64(totalMem) / float64(ceilMemBytes)
	metrics.NamespaceBudgetRequestsRatio.WithLabelValues(namespace, "memory").Set(ratio)

	if totalMem > ceilMemBytes {
		return []string{
			fmt.Sprintf("Current Namespace Memory Allocation: %s", formatBytes(currentMem)),
			fmt.Sprintf("Requested by Pod: %s", formatBytes(proposedMem)),
			fmt.Sprintf("Budget Ceiling: %s", formatBytes(ceilMemBytes)),
			fmt.Sprintf("Exceeded by: %s", formatBytes(totalMem-ceilMemBytes)),
		}, true
	}
	return nil, false
}

// validateNamespaceBudget checks aggregate resource consumption against policy budget ceilings.
func (h *ValidatingHandler) validateNamespaceBudget(ctx context.Context, namespace, podName string, proposedPod *corev1.Pod, pol *tollgatev1alpha1.TollgatePolicy) ([]string, bool, string) {
	budget := pol.Spec.Budget.AggregateRequests
	if budget == nil || h.Client == nil || namespace == "" {
		return nil, false, ""
	}

	var podList corev1.PodList
	if err := h.Client.List(ctx, &podList, client.InNamespace(namespace)); err != nil {
		h.Log.Error(err, "failed to list active pods in namespace for budget check", "namespace", namespace)
		return nil, false, ""
	}

	currentCPU, currentMem := sumActivePods(podList.Items, podName)
	proposedCPU, proposedMem := calculatePodRequests(proposedPod)

	var details []string
	exceeded := false

	if ceilCPU, ok := budget[corev1.ResourceCPU]; ok && !ceilCPU.IsZero() {
		if cpuDetails, cpuExceeded := evaluateCPUBudget(currentCPU, proposedCPU, ceilCPU.MilliValue(), namespace); cpuExceeded {
			exceeded = true
			details = append(details, cpuDetails...)
		}
	}

	if ceilMem, ok := budget[corev1.ResourceMemory]; ok && !ceilMem.IsZero() {
		if memDetails, memExceeded := evaluateMemoryBudget(currentMem, proposedMem, ceilMem.Value(), namespace); memExceeded {
			exceeded = true
			details = append(details, memDetails...)
		}
	}

	return details, exceeded, "BUDGET_EXCEEDED"
}

// formatBytes formats raw bytes into human readable binary unit strings.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// formatRejectionMessage formats denial output according to the project technical specification.
func formatRejectionMessage(namespace, resourceName, reason string, details []string, action string) string {
	var sb strings.Builder
	sb.WriteString("[kube-tollgate] Admission Denied:\n")
	sb.WriteString(fmt.Sprintf("  Namespace: %s\n", namespace))
	sb.WriteString(fmt.Sprintf("  Resource: %s\n", resourceName))
	sb.WriteString(fmt.Sprintf("  Reason: %s\n", reason))
	if len(details) > 0 {
		sb.WriteString("  Details:\n")
		for _, d := range details {
			sb.WriteString(fmt.Sprintf("    - %s\n", d))
		}
	}
	if action != "" {
		sb.WriteString("  Action Required:\n")
		sb.WriteString(fmt.Sprintf("    - %s\n", action))
	}
	return sb.String()
}
