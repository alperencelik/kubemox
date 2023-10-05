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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	Log := log.FromContext(ctx)
	// Get the VirtualMachine resource with this namespace/name
	vm := &proxmoxv1alpha1.VirtualMachine{}
	err := r.Get(ctx, req.NamespacedName, vm)
	if err != nil {
		// Log.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the VirtualMachine instance is marked to be deleted, which is indicated by the deletion timestamp being set.
	if vm.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			controllerutil.AddFinalizer(vm, virtualMachineFinalizerName)
			if err := r.Update(ctx, vm); err != nil {
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vm, virtualMachineFinalizerName) {
			// Delete the VM
			kubernetes.CreateVMKubernetesEvent(vm, kubernetes.Clientset, "Deleting")
			proxmox.DeleteVM(vm.Spec.Name, vm.Spec.NodeName)
			// Remove finalizer
			controllerutil.RemoveFinalizer(vm, virtualMachineFinalizerName)
			if err := r.Update(ctx, vm); err != nil {
				fmt.Printf("Error updating VirtualMachine %s", vm.Spec.Name)
			}

		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	resourceKey := fmt.Sprintf("%s/%s", vm.Namespace, vm.Name)

	// Check if this VirtualMachine already exists
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName
	vmExists := proxmox.CheckVM(vmName, nodeName)
	if vmExists == true {
		// If exists, update the VM
		vmState := proxmox.GetVMState(vmName, nodeName)
		if vmState == "stopped" {
			proxmox.StartVM(vmName, nodeName)
		} else {
			if isProcessed(resourceKey) {
			} else {
				Log.Info(fmt.Sprintf("VirtualMachine %s already exists and running", vmName))
				// Mark it as processed
				processedResources[resourceKey] = true
			}
			proxmox.UpdateVM(vmName, nodeName, vm)
			r.Update(context.Background(), vm)
		}
	} else {
		// If not exists, create the VM
		Log.Info(fmt.Sprintf("VirtualMachine %s doesn't exist", vmName))
		vmType := proxmox.CheckVMType(vm)
		if vmType == "template" {
			kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Creating")
			proxmox.CreateVMFromTemplate(vm)
			proxmox.StartVM(vmName, nodeName)
			kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Created")
		} else if vmType == "scratch" {
			kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Creating")
			proxmox.CreateVMFromScratch(vm)
			proxmox.StartVM(vmName, nodeName)
			kubernetes.CreateVMKubernetesEvent(vm, Clientset, "Created")
		} else {
			Log.Info(fmt.Sprintf("VM %s doesn't have any template or vmSpec defined", vmName))
		}
	}
	// If template and created VM has different resources then update the VM with new resources the function itself decides if VM restart needed or not
	proxmox.UpdateVM(vmName, nodeName, vm)
	r.Update(context.Background(), vm)
	// Update the status of VirtualMachine resource
	var Status proxmoxv1alpha1.VirtualMachineStatus
	Status.State, Status.ID, Status.Uptime, Status.Node, Status.Name, Status.IPAddress, Status.OSInfo = proxmox.UpdateVMStatus(vmName, nodeName)
	vm.Status = Status
	err = r.Status().Update(ctx, vm)
	if err != nil {
		Log.Error(err, "Error updating VirtualMachine status")
	}

	return ctrl.Result{Requeue: true, RequeueAfter: VMreconcilationPeriod * time.Second}, client.IgnoreNotFound(err)

}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {

	log := log.FromContext(context.Background())
	version, err := proxmox.GetProxmoxVersion()
	if err != nil {
		log.Error(err, "Error getting Proxmox version")
	}
	log.Info(fmt.Sprintf("Connected to the Proxmox, version is: %s", version))
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}). // --> This was needed for reconcile loop to work properly, otherwise it was reconciling 3-4 times every 10 seconds
		WithOptions(controller.Options{MaxConcurrentReconciles: VMmaxConcurrentReconciles}).
		Complete(&VirtualMachineReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}

var processedResources = make(map[string]bool)
var logMutex sync.Mutex

func isProcessed(resourceKey string) bool {
	logMutex.Lock()
	defer logMutex.Unlock()
	return processedResources[resourceKey]
}

func markAsProcessed(resourceKey string) {
	logMutex.Lock()
	defer logMutex.Unlock()
	processedResources[resourceKey] = true
}
