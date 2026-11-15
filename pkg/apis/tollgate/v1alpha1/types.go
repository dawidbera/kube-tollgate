// Package v1alpha1 contains API Schema definitions for the finops v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=finops.tollgate.io
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyMode defines whether policy violations are blocked or audited.
// +kubebuilder:validation:Enum=Enforce;Audit
type PolicyMode string

const (
	// PolicyModeEnforce rejects requests that violate policy constraints.
	PolicyModeEnforce PolicyMode = "Enforce"
	// PolicyModeAudit permits requests that violate policy constraints but records warnings/metrics.
	PolicyModeAudit PolicyMode = "Audit"
)

// RequiredLabel defines a mandatory label key and optional regex pattern for value validation.
type RequiredLabel struct {
	// Key is the label key that must exist on the pod.
	Key string `json:"key"`
	// Pattern is an optional regular expression that the label value must match.
	// +optional
	Pattern string `json:"pattern,omitempty"`
}

// AutoInjectionConfig defines rules for injecting missing labels onto workloads.
type AutoInjectionConfig struct {
	// Enabled determines if automatic injection of missing labels is active.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// InheritFromNamespace instructs the webhook to look for matching annotations/labels on parent namespace.
	// +optional
	InheritFromNamespace bool `json:"inheritFromNamespace,omitempty"`
	// Defaults provides fallback values if not inherited from namespace.
	// +optional
	Defaults map[string]string `json:"defaults,omitempty"`
}

// LabelPolicy configures required labels and auto-injection behavior.
type LabelPolicy struct {
	// Required is a list of labels that pods must contain.
	// +optional
	Required []RequiredLabel `json:"required,omitempty"`
	// AutoInject defines auto-injection behaviors for missing required labels.
	// +optional
	AutoInject AutoInjectionConfig `json:"autoInject,omitempty"`
}

// RatioConfig defines the maximum allowable limit-to-request ratio.
type RatioConfig struct {
	// CPU is the maximum allowed ratio of cpu limit to cpu request (limit / request).
	// +optional
	CPU float64 `json:"cpu,omitempty"`
	// Memory is the maximum allowed ratio of memory limit to memory request (limit / request).
	// +optional
	Memory float64 `json:"memory,omitempty"`
}

// ResourcePolicy defines default container requests/limits, ceilings, and ratios.
type ResourcePolicy struct {
	// Defaults specifies baseline requests and limits injected into containers missing them.
	// +optional
	Defaults corev1.ResourceRequirements `json:"defaults,omitempty"`
	// MaxAllowedPerPod defines resource ceilings that no single pod can exceed.
	// +optional
	MaxAllowedPerPod corev1.ResourceList `json:"maxAllowedPerPod,omitempty"`
	// MaxRatio defines the maximum allowed ratio between limit and request.
	// +optional
	MaxRatio RatioConfig `json:"maxRatio,omitempty"`
}

// BudgetQuota defines aggregate resource ceilings for active pods in the namespace.
type BudgetQuota struct {
	// AggregateRequests is the maximum aggregate resource requests allowed across all active pods in the namespace.
	// +optional
	AggregateRequests corev1.ResourceList `json:"aggregateRequests,omitempty"`
}

// TollgatePolicySpec defines the desired state of TollgatePolicy.
type TollgatePolicySpec struct {
	// Mode determines whether violations are blocked (Enforce) or allowed with warnings (Audit).
	// +kubebuilder:validation:Enum=Enforce;Audit
	// +kubebuilder:default=Enforce
	Mode PolicyMode `json:"mode"`

	// Labels defines mandatory cost tag rules and auto-injection defaults.
	// +optional
	Labels LabelPolicy `json:"labels,omitempty"`

	// Resources defines container-level limits, defaults, and ratios.
	// +optional
	Resources ResourcePolicy `json:"resources,omitempty"`

	// Budget defines aggregate resource ceilings for the namespace.
	// +optional
	Budget BudgetQuota `json:"budget,omitempty"`
}

// TollgatePolicyStatus defines the observed state of TollgatePolicy.
type TollgatePolicyStatus struct {
	// ObservedGeneration represents the .metadata.generation that the status was reconciled against.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Phase indicates current policy status (e.g. Active, Error).
	// +optional
	Phase string `json:"phase,omitempty"`
	// Conditions details status conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=tollgatepolicies,scope=Namespaced,shortName=tgp
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// TollgatePolicy is the Schema for the tollgatepolicies API.
type TollgatePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TollgatePolicySpec   `json:"spec,omitempty"`
	Status TollgatePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TollgatePolicyList contains a list of TollgatePolicy.
type TollgatePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TollgatePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TollgatePolicy{}, &TollgatePolicyList{})
}
