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
	"time"

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

	// Define requeue and dontRequeue
	requeue     = ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod * time.Second}
	dontRequeue = ctrl.Result{}
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Watchers *proxmox.ExternalWatchers
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

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
	// Get the Proxmox client reference
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, vm.Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
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
		vmExists, err = pc.CheckVM(vm.Spec.Name, vm.Spec.NodeName)
		if err != nil {
			logger.Error(err, "Error checking VirtualMachine")
			return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
		}
		if !vmExists {
			// If not exists, create the VM
			logger.Info("Creating VirtualMachine", "name", vm.Spec.Name)
			var result ctrl.Result
			result, err = r.CreateVirtualMachine(ctx, pc, vm)
			if err != nil {
				logger.Error(err, "Error creating VirtualMachine")
				return result, err
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
	if vm.DeletionTimestamp.IsZero() {
		err = r.handleFinalizer(ctx, vm)
		if err != nil {
			logger.Error(err, "Error handling finalizer")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			// Delete the VM
			res, delErr := r.handleDelete(ctx, req, pc, vm)
			if delErr != nil {
				logger.Error(delErr, "Error handling VirtualMachine deletion")
				return res, client.IgnoreNotFound(delErr)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	// Handle the VirtualMachine operations, such as create, update
	result, err := r.handleVirtualMachineOperations(ctx, pc, vm)
	if err != nil {
		logger.Error(err, "Error handling VirtualMachine operations")
		// TODO: If I return err here it goes for requeue and errors out as "Reconciler error" so need to return nil to stop the requeue
		return ctrl.Result{}, nil
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	logger.Info(fmt.Sprintf("VirtualMachine %s already exists", vm.Spec.Name))

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (result ctrl.Result, err error) {
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName
	logger := log.FromContext(ctx)
	vmExists, err := pc.CheckVM(vmName, nodeName)
	if err != nil {
		logger.Error(err, "Error checking VirtualMachine")
		return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod}, client.IgnoreNotFound(err)
	}
	if !vmExists {
		// If not exists, create the VM
		logger.Info("Creating VirtualMachine", "name", vmName)
		result, err = r.CreateVirtualMachine(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Error creating VirtualMachine")
			return result, client.IgnoreNotFound(err)
		}
	} else {
		var result *ctrl.Result
		result, err = r.UpdateVirtualMachine(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Error updating VirtualMachine")
			return *result, client.IgnoreNotFound(err)
		}
		err = r.handleCloudInitOperations(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Error handling cloud-init operations")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		err = r.handleAdditionalConfig(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Error handling additional configuration")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		// Check if auto start is enabled
		var res ctrl.Result
		res, err = r.handleAutoStart(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Failed to start VirtualMachine")
			return res, err
		}
	}
	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// TODO: Reduce cyclomatic complexity or refactor the function
// CreateVirtualMachine creates a VirtualMachine on the Proxmox and returns the ctrl.result and error
// The reason for ctrl.Result is to requeue the VirtualMachine based on the error
//
//nolint:all
func (r *VirtualMachineReconciler) CreateVirtualMachine(ctx context.Context, pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	vmType := proxmox.CheckVMType(vm)

	switch vmType {
	case "template":
		r.Recorder.Event(vm, "Normal", "Creating", fmt.Sprintf("VirtualMachine %s is being created", vmName))
		// TODO: Check return err value and based on the error, update the status if needed.
		err := pc.CreateVMFromTemplate(vm)
		// Check if return error is task error
		if err != nil {
			var notFoundErr *proxmox.NotFoundError
			if errors.As(err, &notFoundErr) {
				r.Recorder.Event(vm, "Warning", "Error",
					fmt.Sprintf("VirtualMachine %s failed to create due to %s", vmName, err))
				return dontRequeue, err
			}
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error",
					fmt.Sprintf("VirtualMachine %s failed to create due to %s", vmName, err))
				// It's unrecoverable error, so return the error without requeue but first stop the watcher
				r.StopWatcher(vm.Name) // req.name = object.Name
				return dontRequeue, err
			}
			if updateErr := r.Status().Update(context.Background(), vm); updateErr != nil {
				return requeue, updateErr
			}
		}
		r.Recorder.Event(vm, "Normal", "Created", fmt.Sprintf("VirtualMachine %s has been created", vmName))
		// Before starting the VM handle the additional configuration
		err = r.handleAdditionalConfig(ctx, pc, vm)
		if err != nil {
			logger.Error(err, "Error handling additional configuration")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
		startResult, err := pc.StartVM(vmName, nodeName)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
				r.StopWatcher(vm.Name)
				return dontRequeue, err
			} else {
				logger.Error(err, "Failed to start VirtualMachine")
				return requeue, err
			}
		}
		if startResult {
			logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
		}

	case "scratch":
		r.Recorder.Event(vm, "Normal", "Creating", fmt.Sprintf("VirtualMachine %s is being created", vmName))
		err := pc.CreateVMFromScratch(vm)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error",
					fmt.Sprintf("VirtualMachine %s failed to create due to %s", vmName, err))
				r.StopWatcher(vm.Name)
				return dontRequeue, err
			}
			if updateErr := r.Status().Update(context.Background(), vm); updateErr != nil {
				return requeue, updateErr
			}
		}
		r.Recorder.Event(vm, "Normal", "Created", fmt.Sprintf("VirtualMachine %s has been created", vmName))
		err = r.handleCloudInitOperations(ctx, pc, vm)
		if err != nil {
			return requeue, err
		}

		startResult, err := pc.StartVM(vmName, nodeName)
		if err != nil {
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
				r.StopWatcher(vm.Name)
				return dontRequeue, err
			} else {
				logger.Error(err, "Failed to start VirtualMachine")
				return requeue, err
			}
		}
		if startResult {
			logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
		}
	default:
		return ctrl.Result{}, fmt.Errorf("VM %s doesn't have any template or vmSpec defined", vmName)
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) DeleteVirtualMachine(ctx context.Context,
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Delete the VM
	r.Recorder.Event(vm, "Normal", "Deleting", fmt.Sprintf("VirtualMachine %s is being deleted", vm.Spec.Name))
	if vm.Spec.DeletionProtection {
		logger.Info(fmt.Sprintf("VirtualMachine %s is protected from deletion", vm.Spec.Name))
		return ctrl.Result{}, nil
	} else {
		err := pc.DeleteVM(vm.Spec.Name, vm.Spec.NodeName)
		var notFoundErr *proxmox.NotFoundError
		if errors.As(err, &notFoundErr) {
			logger.Info("VirtualMachine not found, proceeding with finalizer removal")
			return ctrl.Result{}, nil
		}
		if err != nil {
			logger.Error(err, "Failed to delete VirtualMachine")
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to delete due to %s", vm.Spec.Name, err))
				r.StopWatcher(vm.Name)
				return dontRequeue, err
			}
			return requeue, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) UpdateVirtualMachineStatus(ctx context.Context,
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) error {
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type:    typeAvailableVirtualMachine,
		Status:  metav1.ConditionTrue,
		Reason:  "Available",
		Message: "VirtualMachine status is updated",
	})
	// Update the QEMU status
	qemuStatus, err := pc.UpdateVMStatus(vm.Spec.Name, vm.Spec.NodeName)
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
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if vm.Spec.EnableAutoStart {
		vmName := vm.Spec.Name
		nodeName := vm.Spec.NodeName
		vmState, err := pc.GetVMState(vmName, nodeName)
		if err != nil {
			return ctrl.Result{Requeue: true}, err
		}
		if vmState == "stopped" {
			startResult, err := pc.StartVM(vmName, nodeName)
			if err != nil {
				var taskErr *proxmox.TaskError
				if errors.As(err, &taskErr) {
					r.Recorder.Event(vm, "Warning", "Error", fmt.Sprintf("VirtualMachine %s failed to start due to %s", vmName, err))
					r.StopWatcher(vm.Name)
					return dontRequeue, err
				}
				return requeue, err
			}
			if startResult {
				logger.Info(fmt.Sprintf("VirtualMachine %s has been started", vm.Spec.Name))
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineReconciler) UpdateVirtualMachine(ctx context.Context,
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// UpdateVM is checks the delta for CPU and Memory and updates the VM with a restart
	updateStatus, err := pc.UpdateVM(vm)
	if err != nil {
		logger.Error(err, "Failed to update VirtualMachine")
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			r.Recorder.Event(vm, "Warning", "Error",
				fmt.Sprintf("VirtualMachine %s failed to update due to %s", vm.Name, taskErr.ExitStatus))
			r.StopWatcher(vm.Name)
			return &dontRequeue, err
		}
		return &requeue, err
	}
	err = r.UpdateVirtualMachineStatus(ctx, pc, vm)
	if err != nil {
		return &dontRequeue, err
	}
	// ConfigureVirtualMachine is checks the delta for Disk and Network and updates the VM without a restart
	err = pc.ConfigureVirtualMachine(vm)
	if err != nil {
		logger.Error(err, "Failed to configure VirtualMachine")
		var taskErr *proxmox.TaskError
		if errors.As(err, &taskErr) {
			r.Recorder.Event(vm, "Warning", "Error",
				fmt.Sprintf("VirtualMachine %s failed to configure due to %s", vm.Name, taskErr.ExitStatus))
			r.StopWatcher(vm.Name)
			return &dontRequeue, err
		}
		return &requeue, err
	}
	if updateStatus {
		logger.Info(fmt.Sprintf("VirtualMachine %s is updated", vm.Spec.Name))
	}
	return nil, nil
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
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) (ctrl.Result, error) {
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
	}
	// Stop the watcher if resource is being deleted
	r.StopWatcher(req.Name)
	// Perform all operations to delete the VM if the VM is not marked as deleting
	// TODO: Evaluate the requirement of check mechanism for VM whether it's already deleting
	res, err := r.DeleteVirtualMachine(ctx, pc, vm)
	if err != nil {
		logger.Error(err, "Error deleting VirtualMachine")
		return res, client.IgnoreNotFound(err)
	}
	if res != (ctrl.Result{}) {
		return res, nil
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
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) error {
	if proxmox.CheckVMType(vm) == proxmox.VirtualMachineTemplateType {
		return nil
	}
	logger := log.FromContext(ctx)

	if vm.Spec.VMSpec.CloudInitConfig == nil {
		return nil
	}

	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	// TODO: ENable later
	// 1. Add cloud Init CD-ROM drive
	err := pc.AddCloudInitDrive(vmName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to add cloud-init drive to VM")
		return err
	}
	// 2. Set cloud-init configuration
	// err = proxmox.SetCloudInitConfig(vmName, nodeName, vm.Spec.VMSpec.CloudInitConfig)
	// if err != nil {
	// logger.Error(err, "Failed to set cloud-init configuration")
	// return err
	// }
	// If the machine is running, reboot it to apply the cloud-init configuration
	vmState, err := pc.GetVMState(vmName, nodeName)
	if err != nil {
		return err
	}
	if vmState == proxmox.VirtualMachineRunningState {
		err = pc.RebootVM(vmName, nodeName)
		if err != nil {
			logger.Error(err, "Failed to reboot VM")
		}
	}

	return nil
}

func (r *VirtualMachineReconciler) handleAdditionalConfig(ctx context.Context,
	pc *proxmox.ProxmoxClient, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	err := pc.ApplyAdditionalConfiguration(vm)
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
	logger := log.FromContext(ctx)
	// Get the Proxmox client reference
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.VirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return err
	}
	return r.UpdateVirtualMachineStatus(ctx, pc, obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) checkDelta(ctx context.Context, obj proxmox.Resource) (bool, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.VirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return false, err
	}
	return pc.CheckVirtualMachineDelta(obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) handleAutoStartFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.VirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return r.handleAutoStart(ctx, pc, obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) handleReconcileFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}})
}

func (r *VirtualMachineReconciler) IsResourceReady(ctx context.Context, obj proxmox.Resource) (bool, error) {
	logger := log.FromContext(ctx)
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, obj.(*proxmoxv1alpha1.VirtualMachine).Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return false, err
	}
	return pc.IsVirtualMachineReady(obj.(*proxmoxv1alpha1.VirtualMachine))
}

func (r *VirtualMachineReconciler) StopWatcher(name string) {
	if stopChan, exists := r.Watchers.Watchers[name]; exists {
		close(stopChan)
		delete(r.Watchers.Watchers, name)
	}
}
