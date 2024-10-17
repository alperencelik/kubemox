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

	"k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/alperencelik/kubemox/pkg/metrics"
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

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=managedvirtualmachines/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

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
	logger.Info(fmt.Sprintf("Reconciling ManagedVirtualMachine %s", managedVM.Name))

	// Handle the external watcher for the ManagedVirtualMachine
	r.handleWatcher(ctx, req, managedVM)

	// Check if the ManagedVM instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if managedVM.ObjectMeta.DeletionTimestamp.IsZero() {
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
				proxmox.DeleteVM(managedVM.Name, managedVM.Spec.NodeName)
			} else {
				// Remove managedVirtualMachineTag from Managed Virtual Machine to not manage it anymore
				err = proxmox.RemoveVirtualMachineTag(managedVM.Name, managedVM.Spec.NodeName, proxmox.ManagedVirtualMachineTag)
				if err != nil {
					logger.Error(err, "Error removing managedVirtualMachineTag from Managed Virtual Machine")
					return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
				}
			}
			metrics.DecManagedVirtualMachineCount()
		}
		logger.Info("Removing finalizer from ManagedVirtualMachine", "name", managedVM.Name)
		// Remove finalizer
		controllerutil.RemoveFinalizer(managedVM, managedvirtualMachineFinalizerName)
		if err = r.Update(ctx, managedVM); err != nil {
			logger.Info(fmt.Sprintf("Error updating ManagedVirtualMachine %s", managedVM.Name))
		}
		return ctrl.Result{}, nil
	}
	// If EnableAutoStart is true, start the VM if it's stopped
	_, err = r.handleAutoStart(ctx, managedVM)
	if err != nil {
		logger.Error(err, "Error handling auto start")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	// Update ManagedVM
	proxmox.UpdateManagedVM(ctx, managedVM)
	err = r.Update(context.Background(), managedVM)
	if err != nil {
		logger.Info(fmt.Sprintf("ManagedVM %v could not be updated", managedVM.Name))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedVirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Get list of VMs that tagged with managedVirtualMachineTag
	managedVMs := proxmox.GetManagedVMs()
	// Handle ManagedVM creation
	err := r.handleManagedVMCreation(context.Background(), managedVMs)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.ManagedVirtualMachine{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldManagedVM := e.ObjectOld.(*proxmoxv1alpha1.ManagedVirtualMachine)
				newManagedVM := e.ObjectNew.(*proxmoxv1alpha1.ManagedVirtualMachine)
				condition1 := !reflect.DeepEqual(oldManagedVM.Spec, newManagedVM.Spec)
				condition2 := newManagedVM.ObjectMeta.GetDeletionTimestamp().IsZero()
				return condition1 || !condition2
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: ManagedVMmaxConcurrentReconciles}).
		Complete(r)
}

func (r *ManagedVirtualMachineReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("ManagedVirtualMachine resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachine")
	return err
}

func (r *ManagedVirtualMachineReconciler) handleManagedVMCreation(ctx context.Context, managedVMs []string) error {
	logger := log.FromContext(ctx)
	for _, managedVM := range managedVMs {
		// Create ManagedVM that matches with tag of managedVirtualMachineTag value
		logger.Info(fmt.Sprintf("ManagedVM %v found!", managedVM))
		if !proxmox.CheckManagedVMExists(managedVM) {
			logger.Info(fmt.Sprintf("ManagedVM %v does not exist so creating it", managedVM))
			managedVM := proxmox.CreateManagedVM(managedVM)
			err := r.Create(context.Background(), managedVM)
			if err != nil {
				logger.Info(fmt.Sprintf("ManagedVM %v could not be created", managedVM.Name))
				return err
			}
			// Add metrics
			metrics.SetManagedVirtualMachineCPUCores(managedVM.Name, managedVM.Namespace, float64(managedVM.Spec.Cores))
			metrics.SetManagedVirtualMachineMemory(managedVM.Name, managedVM.Namespace, float64(managedVM.Spec.Memory))
			metrics.IncManagedVirtualMachineCount()
		} else {
			logger.Info(fmt.Sprintf("ManagedVM %v already exists", managedVM))
		}
	}
	return nil
}

func (r *ManagedVirtualMachineReconciler) handleAutoStart(ctx context.Context,
	managedVM *proxmoxv1alpha1.ManagedVirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if managedVM.Spec.EnableAutoStart {
		vmName := managedVM.Name
		nodeName := managedVM.Spec.NodeName
		vmState := proxmox.GetVMState(vmName, nodeName)
		if vmState == proxmox.VirtualMachineStoppedState {
			startResult, err := proxmox.StartVM(vmName, nodeName)
			if err != nil {
				logger.Info(fmt.Sprintf("ManagedVirtualMachine %s could not be started", vmName))
				return ctrl.Result{Requeue: true}, err
			} else {
				logger.Info(startResult)
			}
			logger.Info(fmt.Sprintf("ManagedVirtualMachine %s started", vmName))
			return ctrl.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *ManagedVirtualMachineReconciler) handleWatcher(ctx context.Context, req ctrl.Request,
	managedVM *proxmoxv1alpha1.ManagedVirtualMachine) {
	r.Watchers.HandleWatcher(ctx, req, func(ctx context.Context, stopChan chan struct{}) (ctrl.Result, error) {
		return proxmox.StartWatcher(ctx, managedVM, stopChan, r.fetchResource, r.updateStatus,
			r.checkDelta, r.handleAutoStartFunc, r.handleReconcileFunc, r.Watchers.DeleteWatcher)
	})
}

func (r *ManagedVirtualMachineReconciler) fetchResource(ctx context.Context, key client.ObjectKey, obj proxmox.Resource) error {
	return r.Get(ctx, key, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) updateStatus(ctx context.Context, obj proxmox.Resource) error {
	return r.UpdateManagedVirtualMachineStatus(ctx, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) checkDelta(obj proxmox.Resource) (bool, error) {
	return proxmox.CheckManagedVMDelta(obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) handleAutoStartFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.handleAutoStart(ctx, obj.(*proxmoxv1alpha1.ManagedVirtualMachine))
}

func (r *ManagedVirtualMachineReconciler) handleReconcileFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}})
}

func (r *ManagedVirtualMachineReconciler) UpdateManagedVirtualMachineStatus(ctx context.Context,
	managedVM *proxmoxv1alpha1.ManagedVirtualMachine) error {
	// Update ManagedVMStatus
	meta.SetStatusCondition(&managedVM.Status.Conditions, metav1.Condition{
		Type:    typeAvailableVirtualMachine,
		Status:  metav1.ConditionTrue,
		Reason:  "Available",
		Message: "VirtualMachine status is updated",
	})
	managedVMName := managedVM.Name
	nodeName := proxmox.GetNodeOfVM(managedVMName)
	// Update the QEMU status of the ManagedVirtualMachine
	ManagedVMStatus, err := proxmox.UpdateVMStatus(managedVMName, nodeName)
	if err != nil {
		return err
	}
	managedVM.Status.Status = *ManagedVMStatus
	if err := r.Status().Update(ctx, managedVM); err != nil {
		return err
	}
	return nil
}
