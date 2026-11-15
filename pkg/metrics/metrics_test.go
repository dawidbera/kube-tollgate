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
}
