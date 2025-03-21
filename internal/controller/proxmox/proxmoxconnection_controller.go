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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// ProxmoxConnectionReconciler reconciles a ProxmoxConnection object
type ProxmoxConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=proxmoxconnections/finalizers,verbs=update

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

	// Fetch the ProxmoxConnection object and build the ProxmoxClient
	proxmoxConnection := &proxmoxv1alpha1.ProxmoxConnection{}
	if err := r.Get(ctx, req.NamespacedName, proxmoxConnection); err != nil {
		logger.Error(err, "unable to fetch ProxmoxConnection")
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	// Set the ProxmoxClient

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProxmoxConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.ProxmoxConnection{}).
		Named("proxmox-proxmoxconnection").
		Complete(r)
}

func (r *ProxmoxConnectionReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("ProxmoxConnection resource not found. Ignoring since object must be deleted.")
		return nil
	}
	logger.Error(err, "Failed to get ProxmoxConnection")
	return err
}
