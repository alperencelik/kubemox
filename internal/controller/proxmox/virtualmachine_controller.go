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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/metrics"
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
	Scheme *runtime.Scheme
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

	logger.Info(fmt.Sprintf("Reconciling VirtualMachine %s", vm.Name))

	// Check if the VirtualMachine instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if vm.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			controllerutil.AddFinalizer(vm, virtualMachineFinalizerName)
			if err = r.Update(ctx, vm); err != nil {
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			// Delete the VM
			logger.Info("Deleting VirtualMachine", "name", vm.Spec.Name)

			// Update the condition for the VirtualMachine if it is not already deleting
			if !meta.IsStatusConditionPresentAndEqual(vm.Status.Conditions, typeDeletingVirtualMachine, metav1.ConditionUnknown) {
				meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
					Type:    typeDeletingVirtualMachine,
					Status:  metav1.ConditionUnknown,
					Reason:  "Deleting",
					Message: "Deleting VirtualMachine",
				})
				if err = r.Status().Update(ctx, vm); err != nil {
					logger.Error(err, "Error updating VirtualMachine status")
					return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
				}
			} else {
				return ctrl.Result{}, nil
			}
			// Perform all operations to delete the VM if the VM is not marked as deleting
			// TODO: Check if the VM is already deleting
			r.DeleteVirtualMachine(vm)

			// Remove finalizer
			logger.Info("Removing finalizer from VirtualMachine", "name", vm.Spec.Name)

			// Remove finalizer
			controllerutil.RemoveFinalizer(vm, virtualMachineFinalizerName)
			if err = r.Update(ctx, vm); err != nil {
				return ctrl.Result{}, nil
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
	// Refetch the VirtualMachine resource

	// Check if this VirtualMachine already exists
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	vmExists := proxmox.CheckVM(vmName, nodeName)
	if !vmExists {
		// If not exists, create the VM
		logger.Info("Creating VirtualMachine", "name", vmName)
		err = r.CreateVirtualMachine(vm)
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
		metrics.IncVirtualMachineCount()
	} else {
		// Check if auto start is enabled
		r.handleAutoStart(ctx, vm)
		err = r.UpdateVirtualMachine(ctx, vm)
		if err != nil {
			logger.Error(err, "Error updating VirtualMachine")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	logger.Info(fmt.Sprintf("VirtualMachine %s already exists and running", vmName))

	// Update the VirtualMachine status
	err = r.UpdateVirtualMachineStatus(vm)
	if err != nil {
		logger.Error(err, "Error updating VirtualMachine status")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	logger := log.FromContext(context.Background())
	version, err := proxmox.GetProxmoxVersion()
	if err != nil {
		logger.Error(err, "Error getting Proxmox version")
	}
	logger.Info(fmt.Sprintf("Connected to the Proxmox, version is: %s", version))
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldVM := e.ObjectOld.(*proxmoxv1alpha1.VirtualMachine)
				newVM := e.ObjectNew.(*proxmoxv1alpha1.VirtualMachine)
				condition1 := !reflect.DeepEqual(oldVM.Spec, newVM.Spec)
				condition2 := newVM.ObjectMeta.GetDeletionTimestamp().IsZero()
				return condition1 || !condition2
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMmaxConcurrentReconciles}).
		Complete(r)
}

func (r *VirtualMachineReconciler) CreateVirtualMachine(vm *proxmoxv1alpha1.VirtualMachine) error {
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName

	vmType := proxmox.CheckVMType(vm)

	switch vmType {
	case "template":
		kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Creating")
		proxmox.CreateVMFromTemplate(vm)
		if err := r.Status().Update(context.Background(), vm); err != nil {
			return err
		}
		proxmox.StartVM(vmName, nodeName)
		kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Created")
	case "scratch":
		kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Creating")
		proxmox.CreateVMFromScratch(vm)
		proxmox.StartVM(vmName, nodeName)
		kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Created")
	default:
		return fmt.Errorf("VM %s doesn't have any template or vmSpec defined", vmName)
	}
	return nil
}

func (r *VirtualMachineReconciler) DeleteVirtualMachine(vm *proxmoxv1alpha1.VirtualMachine) {
	// Delete the VM
	kubernetes.CreateVMKubernetesEvent(vm, kubernetes.Clientset, "Deleting")
	if vm.Spec.DeletionProtection {
		metrics.DecVirtualMachineCount()
		return
	} else {
		proxmox.DeleteVM(vm.Spec.Name, vm.Spec.NodeName)
		metrics.DecVirtualMachineCount()
	}
}

func (r *VirtualMachineReconciler) UpdateVirtualMachineStatus(vm *proxmoxv1alpha1.VirtualMachine) error {
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type:    typeAvailableVirtualMachine,
		Status:  metav1.ConditionTrue,
		Reason:  "Available",
		Message: "VirtualMachine status is updated",
	})
	// Update the QEMU status
	qemuStatus, err := proxmox.UpdateVMStatus(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	vm.Status.Status = *qemuStatus
	if err := r.Status().Update(context.Background(), vm); err != nil {
		return err
	}
	return nil
}

func (r *VirtualMachineReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("VirtualMachine resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachine")
	return err
}

func (r *VirtualMachineReconciler) handleAutoStart(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) ctrl.Result {
	logger := log.FromContext(ctx)
	if vm.Spec.EnableAutoStart {
		vmName := vm.Spec.Name
		nodeName := vm.Spec.NodeName
		vmState := proxmox.GetVMState(vmName, nodeName)
		if vmState == "stopped" {
			proxmox.StartVM(vmName, nodeName)
			logger.Info(fmt.Sprintf("VirtualMachine %s is started", vmName))
			return ctrl.Result{Requeue: true}
		}
	}
	return ctrl.Result{}
}

func (r *VirtualMachineReconciler) UpdateVirtualMachine(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine) error {
	logger := log.FromContext(ctx)
	updateStatus := proxmox.UpdateVM(vm)
	err := r.UpdateVirtualMachineStatus(vm)
	if err != nil {
		return err
	}
	if updateStatus {
		logger.Info(fmt.Sprintf("VirtualMachine %s is updated", vm.Spec.Name))
	}
	return err
}
