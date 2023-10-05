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
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// VirtualMachineSetReconciler reconciles a VirtualMachineSet object
type VirtualMachineSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// Controller settings
	virtualMachineSetFinalizerName = "virtualmachineset.proxmox.alperen.cloud/finalizer"
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VirtualMachineSet object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *VirtualMachineSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)

	// TODO(user): your logic here
	vmSet := &proxmoxv1alpha1.VirtualMachineSet{}
	err := r.Get(ctx, req.NamespacedName, vmSet)
	if err != nil {
		Log.Error(err, "unable to fetch VirtualMachineSet")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	replicas := vmSet.Spec.Replicas
	vmList := &proxmoxv1alpha1.VirtualMachineList{}
	if err := r.List(ctx, vmList,
		client.InNamespace(req.Namespace),
		// Change that one to metadata.ownerReference
		client.MatchingLabels{"owner": vmSet.Name}); err != nil {
	}

	resourceKey := fmt.Sprintf("%s/%s", vmSet.Namespace, vmSet.Name)

	// Create, Update or Delete VMs
	if len(vmList.Items) < replicas {
		for i := 1; i <= replicas; i++ {
			if isProcessed(resourceKey) {
			} else {
				log.Log.Info(fmt.Sprintf("Creating a new VirtualMachine %s for VirtualMachineSet %s : ", vmSet.Name+"-"+strconv.Itoa(i), vmSet.Name))
				processedResources[resourceKey] = true
			}
			vm := &proxmoxv1alpha1.VirtualMachine{}
			vm = &proxmoxv1alpha1.VirtualMachine{
				ObjectMeta: ctrl.ObjectMeta{
					Name:      vmSet.Name + "-" + strconv.Itoa(i),
					Namespace: vmSet.Namespace,
					Labels: map[string]string{
						"owner": vmSet.Name,
					},
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: vmSet.APIVersion,
						Controller: &[]bool{true}[0],
						Kind:       vmSet.Kind,
						Name:       vmSet.ObjectMeta.Name,
						UID:        vmSet.ObjectMeta.UID,
					},
					},
				},
				Spec: proxmoxv1alpha1.VirtualMachineSpec{
					Name:     vmSet.Name + "-" + strconv.Itoa(i),
					NodeName: vmSet.Spec.NodeName,
					Template: vmSet.Spec.Template,
				},
			}
			// Check if this VM already exists
			err = r.Get(ctx, client.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, &proxmoxv1alpha1.VirtualMachine{})
			if err != nil {
				log.Log.Info("VM does not exist, creating")
				if client.IgnoreNotFound(err) != nil {
					return ctrl.Result{}, client.IgnoreNotFound(err)
				}
				// Create the VM
				err = r.Create(ctx, vm)
				if err != nil {
					return ctrl.Result{}, client.IgnoreNotFound(err)
				}
			}
		}
	} else if len(vmList.Items) > replicas {
		var LastConditionTime time.Time
		if time.Since(LastConditionTime) < 5*time.Second {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		} else {
			for i := len(vmList.Items); i > replicas; i-- {
				// Get the VM name
				vmName := vmSet.Name + "-" + strconv.Itoa(i)
				// nodeName := vmSet.Spec.NodeName
				// Delete the VM
				vm := &proxmoxv1alpha1.VirtualMachine{}
				err = r.Get(ctx, client.ObjectKey{Namespace: vmSet.Namespace, Name: vmName}, vm)
				vmResourceKey := fmt.Sprintf("%s-%s", vm.Namespace, vm.Name)
				if isProcessed(vmResourceKey) {
				} else {
					log.Log.Info(fmt.Sprintf("Deleting VirtualMachine %s for VirtualMachineSet %s ", vmName, vmSet.Name))
					err = r.Delete(ctx, vm)
					if err != nil {
						return ctrl.Result{}, client.IgnoreNotFound(err)
					}
					processedResources[vmResourceKey] = true
				}
			}
		}
		LastConditionTime = time.Now()
	} else {
		// Do nothing
		// log.Log.Info("VMSet has the same number of VMs as replicas")
	}

	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// log.Log.Info("VMs in VMSet: " + strconv.Itoa(len(vmList.Items)))

	if vmSet.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
			controllerutil.AddFinalizer(vmSet, virtualMachineSetFinalizerName)
			if err := r.Update(ctx, vmSet); err != nil {
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
			// Delete the VM in the VMSet
			// Remove finalizer
			controllerutil.RemoveFinalizer(vmSet, virtualMachineSetFinalizerName)
			if err := r.Update(ctx, vmSet); err != nil {
				fmt.Printf("Error updating VirtualMachineSet %s", vmSet.Name)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: 5 * time.Second}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSet{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}). // --> This was needed for reconcile loop to work properly, otherwise it was reconciling 3-4 times every 10 seconds
		WithOptions(controller.Options{MaxConcurrentReconciles: 3}).
		Complete(&VirtualMachineSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}
