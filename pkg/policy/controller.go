package policy

import (
	"context"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tollgatev1alpha1 "github.com/kube-tollgate/kube-tollgate/pkg/apis/tollgate/v1alpha1"
	"github.com/kube-tollgate/kube-tollgate/pkg/metrics"
)

// Reconciler reconciles TollgatePolicy objects and synchronizes them into the in-memory PolicyCache.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cache  Cache
	Log    logr.Logger
}

// Reconcile handles TollgatePolicy events and updates the in-memory PolicyCache.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("tollgatepolicy", req.NamespacedName)

	var policy tollgatev1alpha1.TollgatePolicy
	err := r.Get(ctx, req.NamespacedName, &policy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("policy deleted, removing from cache")
			r.Cache.Delete(req.Namespace, req.Name)
			metrics.PolicySyncTotal.WithLabelValues("deleted").Inc()
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch TollgatePolicy")
		metrics.PolicySyncTotal.WithLabelValues("error").Inc()
		return ctrl.Result{}, err
	}

	if !policy.DeletionTimestamp.IsZero() {
		log.Info("policy marked for deletion, removing from cache")
		r.Cache.Delete(req.Namespace, req.Name)
		metrics.PolicySyncTotal.WithLabelValues("deleted").Inc()
		return ctrl.Result{}, nil
	}

	log.V(1).Info("updating policy in cache", "mode", policy.Spec.Mode)
	r.Cache.Set(&policy)
	metrics.PolicySyncTotal.WithLabelValues("synced").Inc()

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&tollgatev1alpha1.TollgatePolicy{}).
		Complete(r)
}
