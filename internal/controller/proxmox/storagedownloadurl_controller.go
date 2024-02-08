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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/alperencelik/kubemox/pkg/proxmox"
)

// StorageDownloadURLReconciler reconciles a StorageDownloadURL object
type StorageDownloadURLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the StorageDownloadURL object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *StorageDownloadURLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)

	// Fetch the StorageDownloadURL resource
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, req.NamespacedName, storageDownloadURL)
	if err != nil {
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	// Check if the content is only iso or vztmpl
	if storageDownloadURL.Spec.Content != "iso" && storageDownloadURL.Spec.Content != "vztmpl" {
		Log.Info("Content should be either iso or vztmpl")
		return ctrl.Result{}, nil
	}
	// Check if the filename is empty

	// Get fields from the spec
	node := storageDownloadURL.Spec.Node
	proxmox.StorageDownloadURL(node, &storageDownloadURL.Spec)
	return ctrl.Result{}, nil

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StorageDownloadURLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.StorageDownloadURL{}).
		Complete(r)
}
