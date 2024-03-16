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
	"github.com/alperencelik/kubemox/pkg/utils"
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
	Scheme *runtime.Scheme
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
		if errors.IsNotFound(err) {
			logger.Info("ManagedVirtualMachine resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch ManagedVirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger.Info(fmt.Sprintf("Reconciling ManagedVirtualMachine %s", managedVM.Name))

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
			// Delete the VM
			kubernetes.CreateManagedVMKubernetesEvent(managedVM, Clientset, "Deleting")
			proxmox.DeleteVM(managedVM.Name, managedVM.Spec.NodeName)
			metrics.DecManagedVirtualMachineCount()
		}
		logger.Info("Removing finalizer from ManagedVirtualMachine", "name", managedVM.Name)
		// Remove finalizer
		controllerutil.RemoveFinalizer(managedVM, managedvirtualMachineFinalizerName)
		if err = r.Update(ctx, managedVM); err != nil {
			log.Log.Info(fmt.Sprintf("Error updatin ManagedVirtualMachine %s", managedVM.Name))
		}
		return ctrl.Result{}, nil
	}
	// // Update ManagedVM
	proxmox.UpdateManagedVM(managedVM.Name, proxmox.GetNodeOfVM(managedVM.Name), managedVM)
	err = r.Update(context.Background(), managedVM)
	if err != nil {
		log.Log.Info(fmt.Sprintf("ManagedVM %v could not be updated", managedVM.Name))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Update ManagedVMStatus
	managedVMName := managedVM.Name
	nodeName := proxmox.GetNodeOfVM(managedVMName)
	ManagedVMStatus, _ := proxmox.UpdateVMStatus(managedVMName, nodeName)
	managedVM.Status = *ManagedVMStatus
	err = r.Status().Update(context.Background(), managedVM)
	if err != nil {
		log.Log.Info(fmt.Sprintf("ManagedVMStatus %v could not be updated", managedVM.Name))
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedVirtualMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Get all VMs with Proxmox API
	AllVMs := proxmox.GetProxmoxVMs()
	ControllerVMs := proxmox.GetControllerVMs()
	// AllVMs - ControllerVMs = VMs that are not managed by the controller
	ManagedVMs := utils.SubstractSlices(AllVMs, ControllerVMs)
	for _, ManagedVM := range ManagedVMs {
		if !proxmox.CheckManagedVMExists(ManagedVM) {
			log.Log.Info(fmt.Sprintf("ManagedVM %v does not exist so creating it", ManagedVM))
			managedVM := proxmox.CreateManagedVM(ManagedVM)
			err := r.Create(context.Background(), managedVM)
			if err != nil {
				log.Log.Info(fmt.Sprintf("ManagedVM %v could not be created", ManagedVM))
			}
			// Add metrics
			metrics.SetManagedVirtualMachineCPUCores(managedVM.Name, managedVM.Namespace, float64(managedVM.Spec.Cores))
			metrics.SetManagedVirtualMachineMemory(managedVM.Name, managedVM.Namespace, float64(managedVM.Spec.Memory))
		}
		metrics.IncManagedVirtualMachineCount()
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
