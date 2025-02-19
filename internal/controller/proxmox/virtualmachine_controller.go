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
	"reflect"

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
	virtualMachineFinalizerName = "virtualmachine.proxmox.alperen.cloud/finalizer"

	// Controller settings
	VMreconcilationPeriod     = 10
	VMmaxConcurrentReconciles = 10

	// Status conditions
	typeAvailableVirtualMachine = "Available"
	typeCreatingVirtualMachine  = "Creating"
	typeDeletingVirtualMachine  = "Deleting"
	typeErrorVirtualMachine     = "Error"
)

var (
	Clientset, DynamicClient = kubernetes.GetKubeconfig()
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Watchers *proxmox.ExternalWatchers
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *VirtualMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Get the VirtualMachine resource with this namespace/name
	vm := &proxmoxv1alpha1.VirtualMachine{}
	err := r.Get(ctx, req.NamespacedName, vm)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	reconcileMode := kubernetes.GetReconcileMode(vm)

	switch reconcileMode {
	case kubernetes.ReconcileModeWatchOnly:
		logger.Info(fmt.Sprintf("Reconciliation is watch only for VirtualMachine %s", vm.Name))
		r.handleWatcher(ctx, req, vm)
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeEnsureExists:
		logger.Info(fmt.Sprintf("Reconciliation is ensure exists for VirtualMachine %s", vm.Name))
		var vmExists bool
		vmExists, err = proxmox.CheckVM(vm.Spec.Name, vm.Spec.NodeName)
		if err != nil {
			logger.Error(err, "Error checking VirtualMachine")
			return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
		}
		if !vmExists {
			// If not exists, create the VM
			logger.Info("Creating VirtualMachine", "name", vm.Spec.Name)
			err = r.CreateVirtualMachine(ctx, vm)
			if err != nil {
				logger.Error(err, "Error creating VirtualMachine")
			}
		}
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeDisable:
		// Disable the reconciliation
		logger.Info(fmt.Sprintf("Reconciliation is disabled for VirtualMachine %s", vm.Name))
		return ctrl.Result{}, nil
	default:
		// Normal mode
		break
	}

	logger.Info(fmt.Sprintf("Reconciling VirtualMachine %s", vm.Name))

	// Handle the external watcher for the VirtualMachine
	r.handleWatcher(ctx, req, vm)

	// Check if the VirtualMachine instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if vm.ObjectMeta.DeletionTimestamp.IsZero() {
		err = r.handleFinalizer(ctx, vm)
		if err != nil {
			logger.Error(err, "Error handling finalizer")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			// Delete the VM
			res, delErr := r.handleDelete(ctx, req, vm)
			if delErr != nil {
				logger.Error(delErr, "Error handling VirtualMachine deletion")
				return res, client.IgnoreNotFound(delErr)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	// Handle the VirtualMachine operations, such as create, update
	result, err := r.handleVirtualMachineOperations(ctx, vm)
	if err != nil {
		logger.Error(err, "Error handling VirtualMachine operations")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	if result.Requeue {
		return result, nil
	}

	logger.Info(fmt.Sprintf("VirtualMachine %s already exists", vm.Spec.Name))

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	logger := log.FromContext(context.Background())
	version, err := proxmox.GetProxmoxVersion()
	if err != nil {
		logger.Error(err, "Error getting Proxmox version")
	}
	logger.Info(fmt.Sprintf("Connected to the Proxmox, version is: %s", version.Version))
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldVM := e.ObjectOld.(*proxmoxv1alpha1.VirtualMachine)
				newVM := e.ObjectNew.(*proxmoxv1alpha1.VirtualMachine)
				condition1 := !reflect.DeepEqual(oldVM.Spec, newVM.Spec)
				condition2 := newVM.ObjectMeta.GetDeletionTimestamp().IsZero()
				condition3 := !reflect.DeepEqual(oldVM.GetAnnotations(), newVM.GetAnnotations())
				return condition1 || !condition2 || condition3
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMmaxConcurrentReconciles}).
		Complete(r)
}

func (r *VirtualMachineReconciler) handleVirtualMachineOperations(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (result ctrl.Result, err error) {
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName
	logger := log.FromContext(ctx)
	vmExists, err := proxmox.CheckVM(vmName, nodeName)
	if err != nil {
		logger.Error(err, "Error checking VirtualMachine")
		return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
	}
	if !vmExists {
		// If not exists, create the VM
		logger.Info("Creating VirtualMachine", "name", vmName)
		err = r.CreateVirtualMachine(ctx, vm)
		if err != nil {
			logger.Error(err, "Error creating VirtualMachine")
			meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
				Type:    typeErrorVirtualMachine,
				Status:  metav1.ConditionTrue,
				Reason:  "Error",
				Message: fmt.Sprintf("Error creating VirtualMachine: %s", err),
			})
			if err = r.Status().Update(ctx, vm); err != nil {
				logger.Error(err, "Error updating VirtualMachine status")
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		// Check if auto start is enabled
		_, err = r.handleAutoStart(ctx, vm)
		if err != nil {
			logger.Error(err, "Error handling auto start")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		err = r.UpdateVirtualMachine(ctx, vm)
		if err != nil {
			logger.Error(err, "Error updating VirtualMachine")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		err = r.handleCloudInitOperations(ctx, vm)
		if err != nil {
			logger.Error(err, "Error handling cloud-init operations")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		err = r.handleAdditionalConfig(ctx, vm)
		if err != nil {
			logger.Error(err, "Error handling additional configuration")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// TODO: Reduce cyclomatic complexity or refactor the function
//
//nolint:all
func (r *VirtualMachineReconciler) CreateVirtualMachine(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	vmType := proxmox.CheckVMType(vm)

	switch vmType {
	case "template":
		r.Recorder.Event(vm, "Normal", "Creating", fmt.Sprintf("VirtualMachine %s is being created", vmName))
		// TODO: Check return err value and based on the error, update the status if needed.
		err := proxmox.CreateVMFromTemplate(vm)
		// Check if return error is task error
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error",
					fmt.Sprintf("VirtualMachine %s failed to create due to %s", vmName, err))
				return err
			}
			if updateErr := r.Status().Update(context.Background(), vm); updateErr != nil {
				return updateErr
			}
		}
		r.Recorder.Event(vm, "Normal", "Created", fmt.Sprintf("VirtualMachine %s has been created", vmName))
		startResult, err := proxmox.StartVM(vmName, nodeName)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
			} else {
				logger.Error(err, "Failed to start VirtualMachine")
			}
		}
		if startResult {
			logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
		}

	case "scratch":
		r.Recorder.Event(vm, "Normal", "Creating", fmt.Sprintf("VirtualMachine %s is being created", vmName))
		err := proxmox.CreateVMFromScratch(vm)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error",
					fmt.Sprintf("VirtualMachine %s failed to create due to %s", vmName, err))
				return err
			}
			if updateErr := r.Status().Update(context.Background(), vm); updateErr != nil {
				return updateErr
			}
		}
		r.Recorder.Event(vm, "Normal", "Created", fmt.Sprintf("VirtualMachine %s has been created", vmName))
		err = r.handleCloudInitOperations(ctx, vm)
		if err != nil {
			return err
		}

		startResult, err := proxmox.StartVM(vmName, nodeName)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
			} else {
				logger.Error(err, "Failed to start VirtualMachine")
			}
		}
		if startResult {
			logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
		}
		r.Recorder.Event(vm, "Normal", "Created", fmt.Sprintf("VirtualMachine %s has been created", vmName))
	default:
		return fmt.Errorf("VM %s doesn't have any template or vmSpec defined", vmName)
	}
	return nil
}

func (r *VirtualMachineReconciler) DeleteVirtualMachine(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	// Delete the VM
	r.Recorder.Event(vm, "Normal", "Deleting", fmt.Sprintf("VirtualMachine %s is being deleted", vm.Spec.Name))
	if vm.Spec.DeletionProtection {
		logger.Info(fmt.Sprintf("VirtualMachine %s is protected from deletion", vm.Spec.Name))
		return nil
	} else {
		err := proxmox.DeleteVM(vm.Spec.Name, vm.Spec.NodeName)
		if err != nil {
			logger.Error(err, "Failed to delete VirtualMachine")
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to delete due to %s", vm.Spec.Name, err))
			}
			return err
		}
	}
	return nil
}

func (r *VirtualMachineReconciler) UpdateVirtualMachineStatus(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type:    typeAvailableVirtualMachine,
		Status:  metav1.ConditionTrue,
		Reason:  "Available",
		Message: "VirtualMachine status is updated",
	})
	// Update the QEMU status
	qemuStatus, err := proxmox.UpdateVMStatus(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		// Update the status condition
		r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to update status due to %s", vm.Spec.Name, err))
		return err
	}
	vm.Status.Status = qemuStatus
	if err := r.Status().Update(ctx, vm); err != nil {
		return err
	}
	return nil
}

func (r *VirtualMachineReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if kerrors.IsNotFound(err) {
		logger.Info("VirtualMachine resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachine")
	return err
}

func (r *VirtualMachineReconciler) handleAutoStart(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if vm.Spec.EnableAutoStart {
		vmName := vm.Spec.Name
		nodeName := vm.Spec.NodeName
		vmState, err := proxmox.GetVMState(vmName, nodeName)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		if vmState == "stopped" {
			startResult, err := proxmox.StartVM(vmName, nodeName)
			if err != nil {
				var taskErr *proxmox.TaskError
				if errors.As(err, &taskErr) {
					r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
				} else {
					logger.Error(err, "Failed to start VirtualMachine")
				}
			}
			if startResult {
				logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) UpdateVirtualMachine(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	// UpdateVM is checks the delta for CPU and Memory and updates the VM with a restart
	updateStatus, err := proxmox.UpdateVM(vm)
	if err != nil {
		logger.Error(err, "Failed to update VirtualMachine")
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			r.Recorder.Event(vm, "Warning", "Error",
				fmt.Sprintf("VirtualMachine %s failed to update due to %s", vm.Name, taskErr.ExitStatus))
		}
		return err
	}
	err = r.UpdateVirtualMachineStatus(ctx, vm)
	if err != nil {
		return err
	}
	// ConfigureVirtualMachine is checks the delta for Disk and Network and updates the VM without a restart
	err = proxmox.ConfigureVirtualMachine(vm)
	if err != nil {
		return err
	}
	if updateStatus {
		logger.Info(fmt.Sprintf("VirtualMachine %s is updated", vm.Spec.Name))
	}
	return err
}

func (r *VirtualMachineReconciler) handleFinalizer(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
		controllerutil.AddFinalizer(vm, virtualMachineFinalizerName)
		if err := r.Update(ctx, vm); err != nil {
			logger.Error(err, "Error updating VirtualMachine")
			return err
		}
	}
	return nil
}

func (r *VirtualMachineReconciler) handleDelete(ctx context.Context, req ctrl.Request,
	vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Delete the VM
	logger.Info(fmt.Sprintf("Deleting VirtualMachine %s", vm.Spec.Name))

	// Update the condition for the VirtualMachine if it is not already deleting
	if !meta.IsStatusConditionPresentAndEqual(vm.Status.Conditions, typeDeletingVirtualMachine, metav1.ConditionUnknown) {
		meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
			Type:    typeDeletingVirtualMachine,
			Status:  metav1.ConditionUnknown,
			Reason:  "Deleting",
			Message: "Deleting VirtualMachine",
		})
		if err := r.Status().Update(ctx, vm); err != nil {
			logger.Error(err, "Error updating VirtualMachine status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		return ctrl.Result{}, nil
	}
	// Stop the watcher if resource is being deleted
	if stopChan, exists := r.Watchers.Watchers[req.Name]; exists {
		close(stopChan)
		delete(r.Watchers.Watchers, req.Name)
	}
	// Perform all operations to delete the VM if the VM is not marked as deleting
	// TODO: Evaluate the requirement of check mechanism for VM whether it's already deleting
	err := r.DeleteVirtualMachine(ctx, vm)
	if err != nil {
		logger.Error(err, "Failed to delete VirtualMachine")
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			r.Recorder.Event(vm, "Warning", "Error",
				fmt.Sprintf("VirtualMachine %s failed to delete due to %s", vm.Spec.Name, err))
			return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
		}
		if err = r.Status().Update(context.Background(), vm); err != nil {
			return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
		}
	}

	// Remove finalizer
	logger.Info("Removing finalizer from VirtualMachine", "name", vm.Spec.Name)
	controllerutil.RemoveFinalizer(vm, virtualMachineFinalizerName)
	if err := r.Update(ctx, vm); err != nil {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) handleCloudInitOperations(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) error {
	if proxmox.CheckVMType(vm) == proxmox.VirtualMachineTemplateType {
		return nil
	}
	logger := log.FromContext(ctx)

	if vm.Spec.VMSpec.CloudInitConfig == nil {
		return nil
	}

	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	// 1. Add cloud Init CD-ROM drive
	err := proxmox.AddCloudInitDrive(vmName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to add cloud-init drive to VM")
		return err
	}
	// 2. Set cloud-init configuration
	err = proxmox.SetCloudInitConfig(vmName, nodeName, vm.Spec.VMSpec.CloudInitConfig)
	if err != nil {
		logger.Error(err, "Failed to set cloud-init configuration")
		return err
	}
	// If the machine is running, reboot it to apply the cloud-init configuration
	vmState, err := proxmox.GetVMState(vmName, nodeName)
	if err != nil {
		return err
	}
	if vmState == proxmox.VirtualMachineRunningState {
		err = proxmox.RebootVM(vmName, nodeName)
		if err != nil {
			logger.Error(err, "Failed to reboot VM")
		}
	}

	return nil
}

func (r *VirtualMachineReconciler) handleAdditionalConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	err := proxmox.ApplyAdditionalConfiguration(vm)
	if err != nil {
		logger.Error(err, "Failed to apply additional configuration")
		return err
	}
	return nil
}

func (r *VirtualMachineReconciler) handleWatcher(ctx context.Context, req ctrl.Request, vm *proxmoxv1alpha1.VirtualMachine) {
	r.Watchers.HandleWatcher(ctx, req, func(ctx context.Context, stopChan chan struct{}) (ctrl.Result, error) {
		return proxmox.StartWatcher(ctx, vm, stopChan, r.fetchResource, r.updateStatus,
			r.checkDelta, r.handleAutoStartFunc, r.handleReconcileFunc, r.Watchers.DeleteWatcher, r.IsResourceReady)
	})
}

func (r *VirtualMachineReconciler) fetchResource(ctx context.Context, key client.ObjectKey, obj proxmox.Resource) error {
	return r.Get(ctx, key, obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) updateStatus(ctx context.Context, obj proxmox.Resource) error {
	return r.UpdateVirtualMachineStatus(ctx, obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) checkDelta(obj proxmox.Resource) (bool, error) {
	return proxmox.CheckVirtualMachineDelta(obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) handleAutoStartFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.handleAutoStart(ctx, obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) handleReconcileFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}})
}

func (r *VirtualMachineReconciler) IsResourceReady(obj proxmox.Resource) (bool, error) {
	return proxmox.IsVirtualMachineReady(obj.(*proxmoxv1alpha1.VirtualMachine))
}
