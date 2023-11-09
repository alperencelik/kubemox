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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/alperencelik/kubemox/pkg/proxmox"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// ContainerReconciler reconciles a Container object
type ContainerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	containerFinalizerName = "container.proxmox.alperen.cloud/finalizer"
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Container object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *ContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)
	// Get the Container resource with this namespace/name
	container := &proxmoxv1alpha1.Container{}
	err := r.Get(ctx, req.NamespacedName, container)
	if err != nil {
		// Error reading the object - requeue the request.
		Log.Error(err, "Failed to get Container")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName

	resourceKey := fmt.Sprintf("%s/%s", container.Namespace, container.Name)

	// Check if the Container instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if container.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer, then lets add the finalizer and update the object.
		if !controllerutil.ContainsFinalizer(container, containerFinalizerName) {
			controllerutil.AddFinalizer(container, containerFinalizerName)
			if err := r.Update(ctx, container); err != nil {
				Log.Error(err, "Error updating Container")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(container, containerFinalizerName) {
			// Delete the Container
			deletionKey := fmt.Sprintf("%s/%s-deletion", container.Namespace, container.Name)
			if isProcessed(deletionKey) {
			} else {
				// Delete the Container
				containerState := proxmox.GetContainerState(containerName, nodeName)
				log.Log.Info(containerState)
				//proxmox.DeleteContainer(container.Spec.NodeName, container.Spec.Name)
				//processedResources[deletionKey] = true
			}
			// Remove finalizer
			controllerutil.RemoveFinalizer(container, containerFinalizerName)
			if err := r.Update(ctx, container); err != nil {
				Log.Error(err, "Error updating Container")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	containerExists := proxmox.ContainerExists(containerName, nodeName)
	if containerExists {
		// Update Container
		containerState := proxmox.GetContainerState(containerName, nodeName)
		if containerState == "stopped" {
			proxmox.StartContainer(containerName, nodeName)
		} else {
			if isProcessed(resourceKey) {
			} else {
				Log.Info(fmt.Sprintf("Container %s already exists and running", containerName))
				// Mark it as processed
				processedResources[resourceKey] = true
			}
			// Update Container
			// proxmox.UpdateContainer(containerName, nodeName, container)
			// err = r.Update(context.Background(), container)
			// if err != nil {
			// Log.Error(err, "Failed to update Container")
			// return ctrl.Result{}, client.IgnoreNotFound(err)
			// }

		}
	} else {
		// Create Container
		err = proxmox.CloneContainer(container)
		if err != nil {
			Log.Error(err, "Failed to clone Container")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		proxmox.StartContainer(containerName, nodeName)
	}
	return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.Container{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(&ContainerReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}
