package policy

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
)

// TestPolicyReconciler_Reconcile tests the reconciliation lifecycle: add, update, and delete.
func TestPolicyReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, tollgatev1alpha1.AddToScheme(scheme))

	initialPolicy := &tollgatev1alpha1.TollgatePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "finance",
		},
		Spec: tollgatev1alpha1.TollgatePolicySpec{
			Mode: tollgatev1alpha1.PolicyModeEnforce,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initialPolicy).
		Build()

	cache := NewMemoryCache()
	reconciler := &Reconciler{
		Client: fakeClient,
		Scheme: scheme,
		Cache:  cache,
		Log:    logr.Discard(),
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: "finance",
			Name:      "test-policy",
		},
	}

	// 1. Initial Reconcile (Policy Add)
	res, err := reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	cached, found := cache.Get("finance")
	require.True(t, found)
	assert.Equal(t, tollgatev1alpha1.PolicyModeEnforce, cached.Spec.Mode)

	// 2. Policy Update
	initialPolicy.Spec.Mode = tollgatev1alpha1.PolicyModeAudit
	require.NoError(t, fakeClient.Update(context.Background(), initialPolicy))

	res, err = reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	cached, found = cache.Get("finance")
	require.True(t, found)
	assert.Equal(t, tollgatev1alpha1.PolicyModeAudit, cached.Spec.Mode)

	// 3. Policy Marked For Deletion (via Delete with finalizer)
	initialPolicy.SetFinalizers([]string{"tollgate.io/finalizer"})
	require.NoError(t, fakeClient.Update(context.Background(), initialPolicy))
	require.NoError(t, fakeClient.Delete(context.Background(), initialPolicy))

	res, err = reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	_, found = cache.Get("finance")
	assert.False(t, found)

	// 4. Policy Actually Deleted / Not Found
	// Fetch latest version before updating finalizers
	require.NoError(t, fakeClient.Get(context.Background(), req.NamespacedName, initialPolicy))
	initialPolicy.SetFinalizers(nil)
	require.NoError(t, fakeClient.Update(context.Background(), initialPolicy))

	res, err = reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	_, found = cache.Get("finance")
	assert.False(t, found)

	res, err = reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.Requeue)

	_, found = cache.Get("finance")
	assert.False(t, found)
}
