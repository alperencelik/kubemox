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
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/alperencelik/kubemox/pkg/proxmox"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	typeDeletingContainer  = "Deleting"
	typeAvailableContainer = "Available"
	typeCreatingContainer  = "Creating"
	typeErrorContainer     = "Error"
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
	logger := log.FromContext(ctx)
	// Get the Container resource with this namespace/name
	container := &proxmoxv1alpha1.Container{}
	err := r.Get(ctx, req.NamespacedName, container)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	logger.Info(fmt.Sprintf("Reconciling Container %s", container.Name))

	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName

	// Check if the Container instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if container.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer, then lets add the finalizer and update the object.
		err = r.handleFinalizer(ctx, container)
		if err != nil {
			logger.Error(err, "Failed to handle finalizer")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(container, containerFinalizerName) {
			// Delete the Container
			logger.Info("Deleting Container", "name", container.Name)

			if !meta.IsStatusConditionPresentAndEqual(container.Status.Conditions, typeDeletingContainer, metav1.ConditionTrue) {
				meta.SetStatusCondition(&container.Status.Conditions, metav1.Condition{
					Type:    typeDeletingContainer,
					Status:  metav1.ConditionTrue,
					Reason:  "Deleting",
					Message: "Deleting Container",
				})
				if err = r.Status().Update(ctx, container); err != nil {
					logger.Error(err, "Error updating Container status")
					return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
				}
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
			proxmox.DeleteContainer(containerName, nodeName)
			// Remove finalizer
			controllerutil.RemoveFinalizer(container, containerFinalizerName)
			if err = r.Update(ctx, container); err != nil {
				logger.Error(err, "Error updating Container")
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	containerExists := proxmox.ContainerExists(containerName, nodeName)
	if containerExists {
		err = r.StartOrUpdateContainer(ctx, container)
		if err != nil {
			logger.Error(err, "Failed to start or update Container")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		err = r.handleCloneContainer(ctx, container)
		if err != nil {
			logger.Error(err, "Failed to clone Container")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}

	// Update Container Status
	err = r.UpdateContainerStatus(ctx, container)
	if err != nil {
		logger.Error(err, "Failed to update Container status")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.Container{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldContainer := e.ObjectOld.(*proxmoxv1alpha1.Container)
				newContainer := e.ObjectNew.(*proxmoxv1alpha1.Container)
				condition1 := !reflect.DeepEqual(oldContainer.Spec, newContainer.Spec)
				condition2 := newContainer.ObjectMeta.GetDeletionTimestamp().IsZero()
				return condition1 || !condition2
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

func (r *ContainerReconciler) CloneContainer(container *proxmoxv1alpha1.Container) error {
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	err := proxmox.CloneContainer(container)
	if err != nil {
		return err
	}
	err = proxmox.StartContainer(containerName, nodeName)
	if err != nil {
		return err
	}
	return nil
}

func (r *ContainerReconciler) UpdateContainer(ctx context.Context, container *proxmoxv1alpha1.Container) error {
	proxmox.UpdateContainer(container)
	err := r.Update(ctx, container)
	if err != nil {
		return err
	}
	return nil
}

func (r *ContainerReconciler) UpdateContainerStatus(ctx context.Context, container *proxmoxv1alpha1.Container) error {
	containerStatus := proxmox.UpdateContainerStatus(container.Name, container.Spec.NodeName)

	qemuStatus := proxmoxv1alpha1.QEMUStatus{
		State:  containerStatus.State,
		Node:   containerStatus.Node,
		Uptime: containerStatus.Uptime,
		ID:     containerStatus.ID,
	}
	container.Status.Status = qemuStatus
	// // Update Container
	err := r.Status().Update(ctx, container)
	if err != nil {
		return err
	}
	return nil
}

func (r *ContainerReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("Container resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get Container")
	return err
}

func (r *ContainerReconciler) handleFinalizer(ctx context.Context, container *proxmoxv1alpha1.Container) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(container, containerFinalizerName) {
		controllerutil.AddFinalizer(container, containerFinalizerName)
		if err := r.Update(ctx, container); err != nil {
			logger.Error(err, "Error updating Container")
			return err
		}
	}
	return nil
}

func (r *ContainerReconciler) StartOrUpdateContainer(ctx context.Context,
	container *proxmoxv1alpha1.Container) error {
	//
	logger := log.FromContext(ctx)
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	// Update Container
	containerState := proxmox.GetContainerState(containerName, nodeName)
	if containerState == "stopped" {
		err := proxmox.StartContainer(containerName, nodeName)
		if err != nil {
			logger.Error(err, "Failed to start Container")
			return err
		}
	} else {
		logger.Info(fmt.Sprintf("Container %s already exists and running", containerName))
		// Update Container
		err := r.UpdateContainer(ctx, container)
		if err != nil {
			logger.Error(err, "Failed to update Container")
			return err
		}
	}
	return nil
}

func (r *ContainerReconciler) handleCloneContainer(ctx context.Context, container *proxmoxv1alpha1.Container) error {
	//
	logger := log.FromContext(ctx)
	// Create Container
	err := r.CloneContainer(container)
	if err != nil {
		logger.Error(err, "Failed to clone Container")
		return err
	}
	err = proxmox.StartContainer(container.Name, container.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Failed to start Container")
		return err
	}
	return nil
}
