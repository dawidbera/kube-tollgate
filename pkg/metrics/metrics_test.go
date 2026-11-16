package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMetricsDefinitions validates that all required metrics are defined and initialized.
func TestMetricsDefinitions(t *testing.T) {
	assert.NotNil(t, AdmissionRequestsTotal)
	assert.NotNil(t, AdmissionDurationSeconds)
	assert.NotNil(t, MutationsAppliedTotal)
	assert.NotNil(t, RejectionsTotal)
	assert.NotNil(t, NamespaceBudgetRequestsRatio)
	assert.NotNil(t, PolicySyncTotal)

	assert.NotPanics(t, func() {
		AdmissionRequestsTotal.WithLabelValues("mutating", "CREATE", "allowed", "default").Inc()
		AdmissionDurationSeconds.WithLabelValues("validating", "CREATE").Observe(0.015)
		MutationsAppliedTotal.WithLabelValues("injected_cpu_request", "default").Inc()
		RejectionsTotal.WithLabelValues("BUDGET_EXCEEDED", "default", "Pod").Inc()
		NamespaceBudgetRequestsRatio.WithLabelValues("default", "cpu").Set(0.75)
		PolicySyncTotal.WithLabelValues("synced").Inc()
	})
}
