// Package policy implements in-memory caching and evaluation logic for Tollgate policies.
package policy

import (
	"sort"
	"sync"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
)

// Cache defines the thread-safe in-memory cache for Tollgate policies.
type Cache interface {
	// Get returns the active TollgatePolicy for the specified namespace.
	Get(namespace string) (*tollgatev1alpha1.TollgatePolicy, bool)
	// Set stores or updates a TollgatePolicy in the cache.
	Set(policy *tollgatev1alpha1.TollgatePolicy)
	// Delete removes a TollgatePolicy by namespace and name from the cache.
	Delete(namespace, name string)
	// List returns all currently cached policies.
	List() []*tollgatev1alpha1.TollgatePolicy
	// Clear flushes all cached policies.
	Clear()
}

// MemoryCache is a thread-safe in-memory implementation of Cache using sync.RWMutex.
type MemoryCache struct {
	mu sync.RWMutex
	// policies maps namespace -> (policy name -> *TollgatePolicy)
	policies map[string]map[string]*tollgatev1alpha1.TollgatePolicy
}

// NewMemoryCache initializes an empty thread-safe MemoryCache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{
		policies: make(map[string]map[string]*tollgatev1alpha1.TollgatePolicy),
	}
}

// Get returns the active TollgatePolicy for the specified namespace.
// If multiple policies exist in the namespace, it returns the policy with the lexicographically first name for deterministic resolution.
func (c *MemoryCache) Get(namespace string) (*tollgatev1alpha1.TollgatePolicy, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nsPolicies, ok := c.policies[namespace]
	if !ok || len(nsPolicies) == 0 {
		return nil, false
	}

	// Deterministic selection if multiple policies exist in namespace
	if len(nsPolicies) == 1 {
		for _, pol := range nsPolicies {
			return pol.DeepCopy(), true
		}
	}

	names := make([]string, 0, len(nsPolicies))
	for name := range nsPolicies {
		names = append(names, name)
	}
	sort.Strings(names)

	return nsPolicies[names[0]].DeepCopy(), true
}

// Set stores or updates a TollgatePolicy in the cache.
// A deep copy of the policy is stored to prevent external mutation.
func (c *MemoryCache) Set(policy *tollgatev1alpha1.TollgatePolicy) {
	if policy == nil || policy.Namespace == "" || policy.Name == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.policies[policy.Namespace]; !ok {
		c.policies[policy.Namespace] = make(map[string]*tollgatev1alpha1.TollgatePolicy)
	}

	c.policies[policy.Namespace][policy.Name] = policy.DeepCopy()
}

// Delete removes a TollgatePolicy by namespace and name from the cache.
func (c *MemoryCache) Delete(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nsPolicies, ok := c.policies[namespace]
	if !ok {
		return
	}

	delete(nsPolicies, name)
	if len(nsPolicies) == 0 {
		delete(c.policies, namespace)
	}
}

// List returns a slice containing deep copies of all currently cached policies.
func (c *MemoryCache) List() []*tollgatev1alpha1.TollgatePolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*tollgatev1alpha1.TollgatePolicy, 0)
	for _, nsPolicies := range c.policies {
		for _, pol := range nsPolicies {
			result = append(result, pol.DeepCopy())
		}
	}

	return result
}

// Clear removes all cached policies from memory.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.policies = make(map[string]map[string]*tollgatev1alpha1.TollgatePolicy)
}
