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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// ContainerReconciler reconciles a Container object
type ContainerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	EventCh  <-chan event.GenericEvent
}

const (
	containerFinalizerName = "container.proxmox.alperen.cloud/finalizer"

	typeDeletingContainer  = "Deleting"
	typeAvailableContainer = "Available"
	typeStoppedContainer   = "stopped"
	typeCreatingContainer  = "Creating"
	typeErrorContainer     = "Error"
)

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=containers/finalizers,verbs=update

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
	// Get the Proxmox client reference
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, container.Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
	}

	reconcileMode := kubernetes.GetReconcileMode(container)

	switch reconcileMode {
	case kubernetes.ReconcileModeWatchOnly:
		logger.Info(fmt.Sprintf("Reconciling Container %s in WatchOnly mode", container.Name))
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeEnsureExists:
		logger.Info(fmt.Sprintf("Reconciling Container %s in EnsureExists mode", container.Name))
		var containerExists bool
		containerExists, err = pc.ContainerExists(container.Spec.Name, container.Spec.NodeName)
		if err != nil {
			logger.Error(err, "Failed to check if Container exists")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		if !containerExists {
			err = r.handleCloneContainer(ctx, pc, container)
			if err != nil {
				logger.Error(err, "Failed to clone Container")
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeDisable:
		// Disable the reconciliation
		logger.Info(fmt.Sprintf("Reconciling Container %s in Disable mode", container.Name))
		return ctrl.Result{}, nil
	default:
		// Continue with the normal reconciliation
		break
	}

	logger.Info(fmt.Sprintf("Reconciling Container %s", container.Name))

	// Check if the Container instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if container.DeletionTimestamp.IsZero() {
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
			res, delErr := r.handleDelete(ctx, pc, req, container)
			if delErr != nil {
				logger.Error(delErr, "Failed to delete Container")
				return res, client.IgnoreNotFound(delErr)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	result, err := r.handleContainerOperations(ctx, pc, container)
	if err != nil {
		logger.Error(err, "Failed to handle Container operations")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// If EnableAutoStart is true, start the container if it's stopped
	if res, autoErr := r.handleAutoStart(ctx, pc, container); autoErr != nil {
		logger.Error(autoErr, "Error handling auto start")
		return ctrl.Result{Requeue: true}, autoErr
	} else if res != (ctrl.Result{}) {
		return res, nil
	}
	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
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
		WithOptions(controller.Options{MaxConcurrentReconciles: 1})

	if r.EventCh != nil {
		builder = builder.WatchesRawSource(source.Channel(r.EventCh, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}

func (r *ContainerReconciler) CloneContainer(pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) error {
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	r.Recorder.Eventf(container, nil, "Normal", "Creating", "Creating", fmt.Sprintf("Creating Container %s", containerName))
	err := pc.CloneContainer(container)
	if err != nil {
		return err
	}
	err = pc.StartContainer(containerName, nodeName)
	if err != nil {
		return err
	}
	r.Recorder.Eventf(container, nil, "Normal", "Created", "Created", fmt.Sprintf("Created Container %s", containerName))
	return nil
}

func (r *ContainerReconciler) UpdateContainer(ctx context.Context, pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) error {
	if err := pc.UpdateContainer(container); err != nil {
		return err
	}
	r.Recorder.Eventf(container, nil, "Normal", "Updated", "Updated", fmt.Sprintf("Updated Container %s", container.Name))
	err := r.Update(ctx, container)
	if err != nil {
		return err
	}
	return nil
}

func (r *ContainerReconciler) UpdateContainerStatus(ctx context.Context, pc *proxmox.ProxmoxClient,
	container *proxmoxv1alpha1.Container) error {
	containerStatus, err := pc.UpdateContainerStatus(container.Spec.Name, container.Spec.NodeName)
	if err != nil {
		return err
	}

	qemuStatus := proxmoxv1alpha1.QEMUStatus{
		State:  containerStatus.State,
		Node:   containerStatus.Node,
		Uptime: containerStatus.Uptime,
		ID:     containerStatus.ID,
	}
	container.Status.Status = qemuStatus
	// Update Container
	err = r.Status().Update(ctx, container)
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
	pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) error {
	logger := log.FromContext(ctx)
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	// Update Container
	containerState, err := pc.GetContainerState(containerName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to get Container state")
		return err
	}
	if containerState == typeStoppedContainer {
		err := pc.StartContainer(containerName, nodeName)
		if err != nil {
			logger.Error(err, "Failed to start Container")
			return err
		}
	} else {
		logger.Info(fmt.Sprintf("Container %s already exists and running", containerName))
		// Update Container
		err := r.UpdateContainer(ctx, pc, container)
		if err != nil {
			logger.Error(err, "Failed to update Container")
			return err
		}
	}
	return nil
}

func (r *ContainerReconciler) handleCloneContainer(ctx context.Context,
	pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) error {
	logger := log.FromContext(ctx)
	// Create Container
	err := r.CloneContainer(pc, container)
	if err != nil {
		logger.Error(err, "Failed to clone Container")
		return err
	}
	err = pc.StartContainer(container.Spec.Name, container.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Failed to start Container")
		return err
	}
	return nil
}

func (r *ContainerReconciler) handleDelete(ctx context.Context,
	pc *proxmox.ProxmoxClient, _ ctrl.Request, container *proxmoxv1alpha1.Container) (
	ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var err error
	logger.Info("Deleting Container", "name", container.Spec.Name)

	if !meta.IsStatusConditionPresentAndEqual(container.Status.Conditions, typeDeletingContainer, metav1.ConditionUnknown) {
		meta.SetStatusCondition(&container.Status.Conditions, metav1.Condition{
			Type:    typeDeletingContainer,
			Status:  metav1.ConditionUnknown,
			Reason:  "Deleting",
			Message: "Deleting Container",
		})
		if err = r.Status().Update(ctx, container); err != nil {
			logger.Error(err, "Error updating Container status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	// Handle deletion of the Container
	if err = r.handleContainerDeletion(ctx, pc, container); err != nil {
		logger.Error(err, "Error deleting Container from Proxmox")
		return ctrl.Result{Requeue: true}, err
	}
	// Remove finalizer
	logger.Info("Removing finalizer from Container", "name", container.Spec.Name)
	controllerutil.RemoveFinalizer(container, containerFinalizerName)
	if err = r.Update(ctx, container); err != nil {
		logger.Error(err, "Error updating Container")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *ContainerReconciler) handleContainerOperations(ctx context.Context,
	pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	containerExists, err := pc.ContainerExists(container.Spec.Name, container.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Failed to check if Container exists")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	if containerExists {
		err := r.StartOrUpdateContainer(ctx, pc, container)
		if err != nil {
			logger.Error(err, "Failed to start or update Container")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		err := r.handleCloneContainer(ctx, pc, container)
		if err != nil {
			logger.Error(err, "Failed to clone Container")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *ContainerReconciler) handleAutoStart(ctx context.Context,
	pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	if container.Spec.EnableAutoStart {
		containerState, err := pc.GetContainerState(containerName, nodeName)
		if err != nil {
			logger.Error(err, "Failed to get Container state")
			return ctrl.Result{Requeue: true}, err
		}
		if containerState == typeStoppedContainer {
			err := pc.StartContainer(containerName, nodeName)
			if err != nil {
				logger.Error(err, "Failed to start Container")
				return ctrl.Result{Requeue: true}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *ContainerReconciler) handleContainerDeletion(ctx context.Context,
	pc *proxmox.ProxmoxClient, container *proxmoxv1alpha1.Container) error {
	logger := log.FromContext(ctx)
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	r.Recorder.Eventf(container, nil, "Normal", "Deleting", "Deleting", fmt.Sprintf("Deleting Container %s", containerName))
	if container.Spec.DeletionProtection {
		logger.Info(fmt.Sprintf("Container %s is protected from deletion", containerName))
		return nil
	}
	if err := pc.DeleteContainer(containerName, nodeName); err != nil {
		return err
	}
	r.Recorder.Eventf(container, nil, "Normal", "Deleted", "Deleted", fmt.Sprintf("Deleted Container %s", containerName))
	return nil
}
