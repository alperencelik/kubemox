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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info(fmt.Sprintf("Reconciling VirtualMachine %s", vm.Name))

	// Check if the VirtualMachine instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if vm.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			controllerutil.AddFinalizer(vm, virtualMachineFinalizerName)
			if err = r.Update(ctx, vm); err != nil {
				logger.Error(err, "Error updating VirtualMachine")
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			// Delete the VM
			logger.Info("Deleting VirtualMachine", "name", vm.Spec.Name)

			// Update the condition for the VirtualMachine
			meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
				Type:    typeDeletingVirtualMachine,
				Status:  metav1.ConditionUnknown,
				Reason:  "Deleting",
				Message: "Deleting VirtualMachine",
			})
			if err = r.Status().Update(ctx, vm); err != nil {
				logger.Error(err, "Error updating VirtualMachine status")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			// Re-fetch the VirtualMachine resource
			if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
				logger.Error(err, "unable to fetch VirtualMachine")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			// Perform all operations to delete the VM
			r.DeleteVirtualMachine(vm)
			// Re-fetch the VirtualMachine resource
			if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
				logger.Error(err, "unable to fetch VirtualMachine")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
				Type:    typeDeletingVirtualMachine,
				Status:  metav1.ConditionTrue,
				Reason:  "Deleted",
				Message: "VirtualMachine deleted",
			})
			if err = r.Status().Update(ctx, vm); err != nil {
				logger.Error(err, "Error updating VirtualMachine status")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			logger.Info("Removing finalizer from VirtualMachine", "name", vm.Spec.Name)

			// Re-fetch the VirtualMachine resource
			if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
				logger.Error(err, "unable to fetch VirtualMachine")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			// Remove finalizer
			if ok := controllerutil.RemoveFinalizer(vm, virtualMachineFinalizerName); !ok {
				logger.Error(err, "Error removing finalizer from VirtualMachine")
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
			if err = r.Update(ctx, vm); err != nil {
				logger.Error(err, "Error updating VirtualMachine")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Refetch the VirtualMachine resource
	if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		metrics.IncVirtualMachineCount()
	} else {
		// If exists, check if it is running or not
		// If not running, start the VM
		vmState := proxmox.GetVMState(vmName, nodeName)
		if vmState == "stopped" {
			proxmox.StartVM(vmName, nodeName)
		} else {
			logger.Info(fmt.Sprintf("VirtualMachine %s already exists and running", vmName))
		}
	}
	// Re-fetch the VirtualMachine resource
	if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Update the VirtualMachine spec if needed
	proxmox.UpdateVM(vmName, nodeName, vm)
	err = r.Update(context.Background(), vm)
	if err != nil {
		logger.Error(err, "Error updating VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
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
		// --> This was needed for reconcile loop to work properly, otherwise it was reconciling 3-4 times every 10 seconds
		Owns(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
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
	if err := controllerutil.SetControllerReference(vm, vm, r.Scheme); err != nil {
		return err
	}
	return nil
}

func (r *VirtualMachineReconciler) DeleteVirtualMachine(vm *proxmoxv1alpha1.VirtualMachine) {
	// Delete the VM
	kubernetes.CreateVMKubernetesEvent(vm, kubernetes.Clientset, "Deleting")
	proxmox.DeleteVM(vm.Spec.Name, vm.Spec.NodeName)
	metrics.DecVirtualMachineCount()
}
