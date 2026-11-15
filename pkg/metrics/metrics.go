// Package metrics defines the Prometheus telemetry instruments for kube-tollgate.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// AdmissionRequestsTotal counts total admission requests processed.
	AdmissionRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tollgate",
			Subsystem: "admission",
			Name:      "requests_total",
			Help:      "Total number of admission requests processed by kube-tollgate.",
		},
		[]string{"handler", "operation", "decision", "namespace"},
	)

	// AdmissionDurationSeconds measures latency distribution of admission evaluations.
	AdmissionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "tollgate",
			Subsystem: "admission",
			Name:      "duration_seconds",
			Help:      "Latency distribution of admission evaluations in seconds.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"handler", "operation"},
	)

	// MutationsAppliedTotal counts mutations applied to workloads.
	MutationsAppliedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tollgate",
			Name:      "mutations_applied_total",
			Help:      "Count of mutations applied to workload specifications.",
		},
		[]string{"type", "namespace"},
	)

	// RejectionsTotal counts rejected admissions by failure reason.
	RejectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tollgate",
			Name:      "rejections_total",
			Help:      "Count of rejected admissions partitioned by reason and resource kind.",
		},
		[]string{"reason", "namespace", "resource_kind"},
	)

	// NamespaceBudgetRequestsRatio tracks current aggregate requests vs budget ceiling.
	NamespaceBudgetRequestsRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "tollgate",
			Name:      "namespace_budget_requests_ratio",
			Help:      "Ratio of current aggregate requests vs budget ceiling (0.0 - 1.0+).",
		},
		[]string{"namespace", "resource"},
	)

	// PolicySyncTotal counts policy sync and reconciliation cycles.
	PolicySyncTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "tollgate",
			Name:      "policy_sync_total",
			Help:      "Count of policy sync/reconciliation cycles.",
		},
		[]string{"status"},
	)
)

// RegisterMetrics registers all Prometheus metrics with controller-runtime's global registry.
func RegisterMetrics() {
	metrics.Registry.MustRegister(
		AdmissionRequestsTotal,
		AdmissionDurationSeconds,
		MutationsAppliedTotal,
		RejectionsTotal,
		NamespaceBudgetRequestsRatio,
		PolicySyncTotal,
	)
}
