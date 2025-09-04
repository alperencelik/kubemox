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
	"errors"
	"fmt"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

const (
	// virtualMachineFinalizerName is the name of the finalizer
	managedvirtualMachineFinalizerName = "managedvirtualmachine.proxmox.alperen.cloud/finalizer"
	ManagedVMreconcilationPeriod       = 15
	ManagedVMmaxConcurrentReconciles   = 5

	// Status conditions
	typeAvailableManagedVirtualMachine = "Available"
	typeCreatingManagedVirtualMachine  = "Creating"
	typeDeletingManagedVirtualMachine  = "Deleting"
	typeErrorManagedVirtualMachine     = "Error"
)

// ManagedVirtualMachineReconciler reconciles a ManagedVirtualMachine object
type ManagedVirtualMachineReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Watchers *proxmox.ExternalWatchers
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ManagedVirtualMachine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *ManagedVirtualMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// TODO(user): your logic here
	managedVM := &proxmoxv1alpha1.ManagedVirtualMachine{}
	err := r.Get(ctx, req.NamespacedName, managedVM)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	// Setup the ProxmoxClient based on the connection name
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, managedVM.Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
	}

	reconcileMode := kubernetes.GetReconcileMode(managedVM)

	switch reconcileMode {
	case kubernetes.ReconcileModeWatchOnly:
		logger.Info(fmt.Sprintf("Reconciling ManagedVirtualMachine %s in WatchOnly mode", managedVM.Name))
		r.handleWatcher(ctx, req, managedVM)
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeDisable:
		// Disable the reconciliation
		logger.Info(fmt.Sprintf("Reconciliation is disabled for VirtualMachine %s", managedVM.Name))
		return ctrl.Result{}, nil
	default:
		// Normal mode
		break
	}

	logger.Info(fmt.Sprintf("Reconciling ManagedVirtualMachine %s", managedVM.Name))

	// Handle the external watcher for the ManagedVirtualMachine
	r.handleWatcher(ctx, req, managedVM)

	// Check if the ManagedVM instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if managedVM.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(managedVM, managedvirtualMachineFinalizerName) {
			controllerutil.AddFinalizer(managedVM, managedvirtualMachineFinalizerName)
			if err = r.Update(ctx, managedVM); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(managedVM, managedvirtualMachineFinalizerName) {
			logger.Info(fmt.Sprintf("Deleting ManagedVirtualMachine %s", managedVM.Name))
			// Delete ManagedVM
			res, delErr := r.handleDelete(ctx, req, pc, managedVM)
			if delErr != nil {
				logger.Error(delErr, "unable to delete ManagedVirtualMachine")
				return res, delErr
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
	// If EnableAutoStart is true, start the VM if it's stopped
	_, err = r.handleAutoStart(ctx, pc, managedVM)
	if err != nil {
		logger.Error(err, "Error handling auto start")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	// Update ManagedVM
	if err = pc.UpdateManagedVM(ctx, managedVM); err != nil {
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			r.Recorder.Event(managedVM, "Warning", "Error",
				fmt.Sprintf("ManagedVirtualMachine %s failed to update due to %s", managedVM.Name, taskErr.ExitStatus))
		}
		logger.Error(err, "Failed to update ManagedVirtualMachine")
		return ctrl.Result{Requeue: true, RequeueAfter: ManagedVMreconcilationPeriod}, client.IgnoreNotFound(err)
	}
	if err = r.Update(context.Background(), managedVM); err != nil {
		logger.Info(fmt.Sprintf("ManagedVM %v could not be updated", managedVM.Name))
		return ctrl.Result{Requeue: true, RequeueAfter: ManagedVMreconcilationPeriod}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedVirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Get all ProxmoxConnection objects

	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.ManagedVirtualMachine{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				return e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration()
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: ManagedVMmaxConcurrentReconciles}).
		Complete(r)
}

func (r *ManagedVirtualMachineReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if kerrors.IsNotFound(err) {
		logger.Info("ManagedVirtualMachine resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachine")
	return err
}

// Deprecated: This function is not used anymore
// func (r *ManagedVirtualMachineReconciler) handleManagedVMCreation(ctx context.Context,
// pc *proxmox.ProxmoxClient, managedVMs []string) error {
// logger := log.FromContext(ctx)
// for _, managedVM := range managedVMs {
// // Create ManagedVM that matches with tag of managedVirtualMachineTag value
// logger.Info(fmt.Sprintf("ManagedVM %v found!", managedVM))
// managedVMExists, err := proxmox.CheckManagedVMExists(managedVM)
// if err != nil {
// return err
// }
// if !managedVMExists {
// logger.Info(fmt.Sprintf("ManagedVM %v does not exist so creating it", managedVM))
// managedVM, err := pc.CreateManagedVM(managedVM)
// if err != nil {
// return err
// }
// err = r.Create(context.Background(), managedVM)
// r.Recorder.Event(managedVM, "Normal", "Creating", fmt.Sprintf("Creating ManagedVirtualMachine %s", managedVM.Name))
// if err != nil {
// logger.Error(err, fmt.Sprintf("ManagedVM %v could not be created", managedVM.Name))
// return err
// }
// // Add metrics and events
// r.Recorder.Event(managedVM, "Normal", "Created", fmt.Sprintf("ManagedVirtualMachine %s created", managedVM.Name))
// } else {
// logger.Info(fmt.Sprintf("ManagedVM %v already exists", managedVM))
// }
// }
// return nil
// }

func (r *ManagedVirtualMachineReconciler) handleAutoStart(ctx context.Context,
	pc *proxmox.ProxmoxClient, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if managedVM.Spec.EnableAutoStart {
		vmName := managedVM.Name
		nodeName := managedVM.Spec.NodeName
		vmState, err := pc.GetVMState(vmName, nodeName)
		if err != nil {
			logger.Info(fmt.Sprintf("Couldn't get the state of the managed Virtual Machine %s", vmName))
			return ctrl.Result{Requeue: true}, err
		}
		if vmState == proxmox.VirtualMachineStoppedState {
			startResult, err := pc.StartVM(vmName, nodeName)
			if err != nil {
				var taskErr *proxmox.TaskError
				if errors.As(err, &taskErr) {
					// Handle the specific TaskError
					r.Recorder.Event(managedVM, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, taskErr.ExitStatus))
				} else {
					logger.Error(err, "Failed to start VirtualMachine")
				}
			}
			if startResult {
				logger.Info(fmt.Sprintf("VirtualMachine %s has been started", managedVM.Spec.Name))
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *ManagedVirtualMachineReconciler) handleWatcher(ctx context.Context, req ctrl.Request,
	managedVM *proxmoxv1alpha1.ManagedVirtualMachine) {
	r.Watchers.HandleWatcher(ctx, req, func(ctx context.Context, stopChan chan struct{}) (ctrl.Result, error) {
		return proxmox.StartWatcher(ctx, managedVM, stopChan, r.fetchResource, r.updateStatus,
			r.checkDelta, r.handleAutoStartFunc, r.handleReconcileFunc, r.Watchers.DeleteWatcher, r.IsResourceReady)
	})
}

func (r *ManagedVirtualMachineReconciler) fetchResource(ctx context.Context, key client.ObjectKey, obj proxmox.Resource) error {
	return r.Get(ctx, key, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) updateStatus(ctx context.Context, obj proxmox.Resource) error {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return err
	}
	return r.UpdateManagedVirtualMachineStatus(ctx, pc, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) checkDelta(ctx context.Context, obj proxmox.Resource) (bool, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return false, err
	}
	return pc.CheckManagedVMDelta(obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) handleAutoStartFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
	}
	return r.handleAutoStart(ctx, pc, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) handleReconcileFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}})
}

func (r *ManagedVirtualMachineReconciler) UpdateManagedVirtualMachineStatus(ctx context.Context,
	pc *proxmox.ProxmoxClient, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) error {
	// Update ManagedVMStatus
	meta.SetStatusCondition(&managedVM.Status.Conditions, metav1.Condition{
		Type:    typeAvailableVirtualMachine,
		Status:  metav1.ConditionTrue,
		Reason:  "Available",
		Message: "VirtualMachine status is updated",
	})
	managedVMName := managedVM.Name
	nodeName, err := pc.GetNodeOfVM(managedVMName)
	if err != nil {
		return err
	}
	// Update the QEMU status of the ManagedVirtualMachine
	ManagedVMStatus, err := pc.UpdateVMStatus(managedVMName, nodeName)
	if err != nil {
		return err
	}
	managedVM.Status.Status = ManagedVMStatus
	if err := r.Status().Update(ctx, managedVM); err != nil {
		return err
	}
	return nil
}

func (r *ManagedVirtualMachineReconciler) IsResourceReady(ctx context.Context, obj proxmox.Resource) (bool, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return false, err
	}
	return pc.IsVirtualMachineReady(obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) handleDelete(ctx context.Context, req ctrl.Request,
	pc *proxmox.ProxmoxClient, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) (
	ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var err error

	if !meta.IsStatusConditionPresentAndEqual(managedVM.Status.Conditions, typeDeletingManagedVirtualMachine, metav1.ConditionTrue) {
		meta.SetStatusCondition(&managedVM.Status.Conditions, metav1.Condition{
			Type:    typeDeletingManagedVirtualMachine,
			Status:  metav1.ConditionTrue,
			Reason:  "Deleting",
			Message: "Deleting ManagedVirtualMachine",
		})
		if err = r.Status().Update(ctx, managedVM); err != nil {
			logger.Error(err, "Error updating ManagedVirtualMachine status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	// Stop the watcher if resource is being deleted
	if stopChan, exists := r.Watchers.Watchers[req.Name]; exists {
		close(stopChan)
		delete(r.Watchers.Watchers, req.Name)
	}

	// Delete the VM
	r.Recorder.Event(managedVM, "Normal", "Deleting", fmt.Sprintf("Deleting ManagedVirtualMachine %s", managedVM.Name))
	if !managedVM.Spec.DeletionProtection {
		err = pc.DeleteVM(managedVM.Name, managedVM.Spec.NodeName)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(managedVM, "Warning", "Error", fmt.Sprintf("ManagedVirtualMachine %s failed to delete due to %s", managedVM.Name, err))
			}
			logger.Error(err, "Error deleting Managed Virtual Machine")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		// Remove managedVirtualMachineTag from Managed Virtual Machine to not manage it anymore
		err = pc.RemoveVirtualMachineTag(managedVM.Name, managedVM.Spec.NodeName, proxmox.ManagedVirtualMachineTag)
		if err != nil {
			logger.Error(err, "Error removing managedVirtualMachineTag from Managed Virtual Machine")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	logger.Info("Removing finalizer from ManagedVirtualMachine", "name", managedVM.Name)
	// Remove finalizer
	controllerutil.RemoveFinalizer(managedVM, managedvirtualMachineFinalizerName)
	if err = r.Update(ctx, managedVM); err != nil {
		logger.Info(fmt.Sprintf("Error updating ManagedVirtualMachine %s", managedVM.Name))
	}
	return ctrl.Result{}, nil
}
