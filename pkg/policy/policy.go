// Package policy implements in-memory caching and evaluation logic for Tollgate policies.
package policy

import (
	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
)

// Engine defines the interface for evaluating workload admission against FinOps policies.
type Engine interface {
	// Name returns the identifier of the policy engine.
	Name() string
	// GetPolicy returns the active policy for the specified namespace.
	GetPolicy(namespace string) (*tollgatev1alpha1.TollgatePolicy, bool)
	// Cache returns the underlying policy cache.
	Cache() Cache
}

// DefaultEngine implements Engine using the in-memory Cache.
type DefaultEngine struct {
	cache Cache
}

// NewEngine creates a new DefaultEngine with the provided Cache.
func NewEngine(c Cache) *DefaultEngine {
	if c == nil {
		c = NewMemoryCache()
	}
	return &DefaultEngine{
		cache: c,
	}
}

// Name returns the identifier of the default engine.
func (e *DefaultEngine) Name() string {
	return "default-finops-engine"
}

// GetPolicy retrieves the active policy for the namespace from cache.
func (e *DefaultEngine) GetPolicy(namespace string) (*tollgatev1alpha1.TollgatePolicy, bool) {
	return e.cache.Get(namespace)
}

// Cache returns the underlying cache instance.
func (e *DefaultEngine) Cache() Cache {
	return e.cache
}
