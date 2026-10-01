/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxmox

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// ProxmoxConnectionReconciler reconciles a ProxmoxConnection object
type ProxmoxConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ProxmoxConnection object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *ProxmoxConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ProxmoxConnection instance
	proxmoxConnection := &proxmoxv1alpha1.ProxmoxConnection{}
	if err := r.Get(ctx, req.NamespacedName, proxmoxConnection); err != nil {
		logger.Error(err, "unable to fetch ProxmoxConnection")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	reconcileMode := kubernetes.GetReconcileMode(proxmoxConnection)

	switch reconcileMode {
	case kubernetes.ReconcileModeDisable:
		logger.Info(fmt.Sprintf("Reconciliation is disabled for ProxmoxConnection %s", proxmoxConnection.Name))
		return ctrl.Result{}, nil
	default:
		break
	}

	logger.Info("Reconciling ProxmoxConnection", "name", proxmoxConnection.Name)

	// Resolve credentials, which may be read from Secrets
	creds, err := proxmox.ResolveCredentials(ctx, r.Client, &proxmoxConnection.Spec)
	if err != nil {
		logger.Error(err, "unable to resolve credentials")
		patch := client.MergeFrom(proxmoxConnection.DeepCopy())
		meta.SetStatusCondition(&proxmoxConnection.Status.Conditions, metav1.Condition{
			LastTransitionTime: metav1.Now(),
			Type:               conditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "CredentialsUnavailable",
			Message:            err.Error(),
		})
		if patchErr := r.Status().Patch(ctx, proxmoxConnection, patch); patchErr != nil {
			logger.Error(patchErr, "unable to update ProxmoxConnection status")
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, err
	}

	// Create Proxmox client
	proxmoxClient := proxmox.NewProxmoxClientWithCredentials(proxmoxConnection, creds)

	// Return the version
	version, err := proxmoxClient.GetVersion()
	if err != nil {
		logger.Error(err, "unable to get version")
		// Update the status with the connection error. The Secret versions are
		// recorded here too: they say what the controller read, not whether
		// Proxmox accepted it.
		patch := client.MergeFrom(proxmoxConnection.DeepCopy())
		proxmoxConnection.Status.ObservedSecrets = creds.ObservedSecrets
		meta.SetStatusCondition(&proxmoxConnection.Status.Conditions, metav1.Condition{
			LastTransitionTime: metav1.Now(),
			Type:               conditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ProxmoxConnectionError",
			Message:            err.Error(),
		})
		if err := r.Status().Patch(ctx, proxmoxConnection, patch); err != nil {
			logger.Error(err, "unable to update ProxmoxConnection status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	logger.Info("Proxmox connection successful", "version", *version)
	// Update the status with the version
	patch := client.MergeFrom(proxmoxConnection.DeepCopy())
	proxmoxConnection.Status.Version = *version
	proxmoxConnection.Status.ObservedSecrets = creds.ObservedSecrets
	// Update the status with the connection status
	meta.SetStatusCondition(&proxmoxConnection.Status.Conditions, metav1.Condition{
		LastTransitionTime: metav1.Now(),
		Type:               conditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ProxmoxConnectionReady",
		Message:            "Proxmox connection is ready",
	})
	if err := r.Status().Patch(ctx, proxmoxConnection, patch); err != nil {
		logger.Error(err, "unable to update ProxmoxConnection status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProxmoxConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Only spec changes; status writes would otherwise loop.
		For(&proxmoxv1alpha1.ProxmoxConnection{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		// Metadata only, so Secret data is never cached; a rotated Secret
		// reconciles the connections using it, which records the new
		// resourceVersion in status.observedSecrets and rebuilds their clients.
		WatchesMetadata(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.connectionsForSecret)).
		Named("proxmox-proxmoxconnection").
		Complete(r)
}

// connectionsForSecret maps a Secret to the ProxmoxConnections that read a credential from it.
func (r *ProxmoxConnectionReconciler) connectionsForSecret(ctx context.Context, secret client.Object) []reconcile.Request {
	conns := &proxmoxv1alpha1.ProxmoxConnectionList{}
	if err := r.List(ctx, conns); err != nil {
		log.FromContext(ctx).Error(err, "unable to list ProxmoxConnections for Secret",
			"secret", client.ObjectKeyFromObject(secret))
		return nil
	}
	var reqs []reconcile.Request
	for _, c := range conns.Items {
		for _, ref := range []*proxmoxv1alpha1.SecretKeyReference{c.Spec.PasswordFrom, c.Spec.SecretFrom} {
			if ref != nil && ref.Namespace == secret.GetNamespace() && ref.Name == secret.GetName() {
				reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKey{Name: c.Name}})
				break
			}
		}
	}
	return reqs
}
