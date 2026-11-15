package policy

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
)

// TestPolicyCache_CRUD validates create, read, update, list, and delete operations on MemoryCache.
func TestPolicyCache_CRUD(t *testing.T) {
	c := NewMemoryCache()

	// Initially empty
	pol, found := c.Get("prod")
	assert.False(t, found)
	assert.Nil(t, pol)
	assert.Empty(t, c.List())

	// Insert policy
	p1 := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-b",
			Namespace: "prod",
		},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
		},
	}
	c.Set(p1)

	pol, found = c.Get("prod")
	require.True(t, found)
	assert.Equal(t, "policy-b", pol.Name)
	assert.Equal(t, tollgatev1alpha1.PolicyModeEnforce, pol.Spec.Mode)

	// Verify deep copy protection
	pol.Spec.Mode = tollgatev1alpha1.PolicyModeAudit
	polCached, _ := c.Get("prod")
	assert.Equal(t, tollgatev1alpha1.PolicyModeEnforce, polCached.Spec.Mode)

	// Add second policy in same namespace: deterministic resolution selects first sorted name ("policy-a")
	p2 := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-a",
			Namespace: "prod",
		},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeAudit,
		},
	}
	c.Set(p2)

	pol, found = c.Get("prod")
	require.True(t, found)
	assert.Equal(t, "policy-a", pol.Name)
	assert.Equal(t, tollgatev1alpha1.PolicyModeAudit, pol.Spec.Mode)

	// List policies
	list := c.List()
	assert.Len(t, list, 2)

	// Delete policy-a
	c.Delete("prod", "policy-a")
	pol, found = c.Get("prod")
	require.True(t, found)
	assert.Equal(t, "policy-b", pol.Name)

	// Delete policy-b
	c.Delete("prod", "policy-b")
	pol, found = c.Get("prod")
	assert.False(t, found)
	assert.Nil(t, pol)

	// Nil handling
	c.Set(nil)
	c.Set(&tollgatev1alpha1.TollgatePolicy{})
	assert.Empty(t, c.List())
}

// TestPolicyCache_ConcurrentAccess validates thread-safety under heavy concurrent reads and writes.
func TestPolicyCache_ConcurrentAccess(t *testing.T) {
	c := NewMemoryCache()
	require.NotNil(t, c)
	const numGoroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ns := fmt.Sprintf("ns-%d", id%5)
			for j := 0; j < iterations; j++ {
				c.Set(&tollgatev1alpha1.TollgatePolicy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("policy-%d", id),
						Namespace: ns,
					},
					Spec: tollgatev1alpha1.TollgatePolicySpec{
						Mode: tollgatev1alpha1.PolicyModeEnforce,
					},
				})
				if j%10 == 0 {
					c.Delete(ns, fmt.Sprintf("policy-%d", id))
				}
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			ns := fmt.Sprintf("ns-%d", id%5)
			for j := 0; j < iterations; j++ {
				_, _ = c.Get(ns)
				_ = c.List()
			}
		}(i)
	}

	wg.Wait()
}

// TestDefaultEngine validates the Engine wrapper around the cache.
func TestDefaultEngine(t *testing.T) {
	c := NewMemoryCache()
	engine := NewEngine(c)

	assert.Equal(t, "default-finops-engine", engine.Name())
	assert.Equal(t, c, engine.Cache())

	p := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eng-pol",
			Namespace: "team-a",
		},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
		},
	}
	c.Set(p)

	pol, found := engine.GetPolicy("team-a")
	require.True(t, found)
	assert.Equal(t, "eng-pol", pol.Name)
}
