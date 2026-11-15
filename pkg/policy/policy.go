// Package policy implements in-memory caching and evaluation logic for Tollgate policies.
package policy

// Engine defines the interface for evaluating workload admission against FinOps policies.
type Engine interface {
	// Name returns the identifier of the policy engine.
	Name() string
}

// DefaultEngine is the standard implementation of Engine.
type DefaultEngine struct{}

// Name returns the human-readable identifier of the default engine.
func (e *DefaultEngine) Name() string {
	return "default-finops-engine"
}
