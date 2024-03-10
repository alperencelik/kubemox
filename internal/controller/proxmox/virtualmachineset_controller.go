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
)

// VirtualMachineSetReconciler reconciles a VirtualMachineSet object
type VirtualMachineSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// Controller settings
	virtualMachineSetFinalizerName = "virtualmachineset.proxmox.alperen.cloud/finalizer"
	VMSetreconcilationPeriod       = 10
	VMSetmaxConcurrentReconciles   = 5

	typeAvailableVirtualMachineSet   = "Available"
	typeScalingUpVirtualMachineSet   = "ScalingUp"
	typeScalingDownVirtualMachineSet = "ScalingDown"
	typeDeletingVirtualMachineSet    = "Deleting"
	typeErrorVirtualMachineSet       = "Error"
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
	logger := log.FromContext(ctx)

	// TODO(user): your logic here
	vmSet := &proxmoxv1alpha1.VirtualMachineSet{}
	err := r.Get(ctx, req.NamespacedName, vmSet)
	if err != nil {
		logger.Error(err, "unable to fetch VirtualMachineSet")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	replicas := vmSet.Spec.Replicas
	vmList := &proxmoxv1alpha1.VirtualMachineList{}
	listOptions := []client.ListOption{
		client.InNamespace(vmSet.Namespace),
		client.MatchingLabels{"owner": vmSet.Name},
	}
	err = r.List(ctx, vmList, listOptions...)
	if err != nil {
		logger.Error(err, "unable to list VirtualMachines")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if vmSet.Status.Conditions == nil || len(vmSet.Status.Conditions) == 0 {
		meta.SetStatusCondition(&vmSet.Status.Conditions, metav1.Condition{
			Type:    typeAvailableVirtualMachineSet,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		err = r.Status().Update(ctx, vmSet)
		if err != nil {
			logger.Error(err, "Error updating VirtualMachineSet status")
		}
	}

	// Re-fetch the VirtualMachineSet resource
	vmSet = &proxmoxv1alpha1.VirtualMachineSet{}
	err = r.Get(ctx, req.NamespacedName, vmSet)
	if err != nil {
		logger.Error(err, "unable to fetch VirtualMachineSet")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// DELETE
	if vmSet.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
			controllerutil.AddFinalizer(vmSet, virtualMachineSetFinalizerName)
			if err = r.Update(ctx, vmSet); err != nil {
				logger.Info(fmt.Sprintf("Error updating VirtualMachineSet %s", vmSet.Name))
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
			// Ensure that the pre-delete logic is idempotent.
			logger.Info(fmt.Sprintf("Deleting VirtualMachineSet %s", vmSet.Name))
			meta.SetStatusCondition(&vmSet.Status.Conditions, metav1.Condition{
				Type:    "Deleting",
				Status:  metav1.ConditionUnknown,
				Reason:  "Deleting",
				Message: "Deleting VirtualMachineSet",
			})
			if err = r.Status().Update(ctx, vmSet); err != nil {
				logger.Info("Error updating VirtualMachineSet status")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			// Get VM list and delete them
			for _, vm := range vmList.Items {
				if err = r.Delete(ctx, &vm); err != nil {
					logger.Error(err, "unable to delete VirtualMachine")
					return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
				}
			}

			if len(vmList.Items) == 0 {
				// Remove finalizer
				if ok := controllerutil.RemoveFinalizer(vmSet, virtualMachineSetFinalizerName); !ok {
					logger.Error(err, "Error removing finalizer from VirtualMachineSet")
				}
				if err = r.Update(ctx, vmSet); err != nil {
					logger.Error(err, "Error updating VirtualMachineSet")
				}
				return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Get VM list and create them
	vmList = &proxmoxv1alpha1.VirtualMachineList{}
	listOptions = []client.ListOption{
		client.InNamespace(vmSet.Namespace),
		client.MatchingLabels{"owner": vmSet.Name},
	}
	err = r.List(ctx, vmList, listOptions...)
	if err != nil {
		logger.Error(err, "unable to list VirtualMachines")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if len(vmList.Items) < int(replicas) {
		if err := r.scaleUpVMs(vmSet, replicas, vmList); err != nil {
			logger.Error(err, "unable to scale up VirtualMachines")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		meta.SetStatusCondition(&vmSet.Status.Conditions, metav1.Condition{
			Type:    typeScalingUpVirtualMachineSet,
			Status:  metav1.ConditionTrue,
			Reason:  "ScaledUp",
			Message: "VirtualMachines scaled up",
		})
		if err = r.Status().Update(ctx, vmSet); err != nil {
			logger.Error(err, "Error updating VirtualMachineSet status")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	if len(vmList.Items) > int(replicas) {
		if err := r.scaleDownVMs(ctx, replicas, vmList); err != nil {
			logger.Error(err, "unable to scale down VirtualMachines")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		// Set the condition for the VirtualMachineSet
		meta.SetStatusCondition(&vmSet.Status.Conditions, metav1.Condition{
			Type:    typeScalingDownVirtualMachineSet,
			Status:  metav1.ConditionTrue,
			Reason:  "ScaledDown",
			Message: "VirtualMachines scaled down",
		})
		if err = r.Status().Update(ctx, vmSet); err != nil {
			logger.Error(err, "Error updating VirtualMachineSet status")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	if err := r.updateVMs(ctx, vmSet, vmList); err != nil {
		logger.Error(err, "unable to update VirtualMachines")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSet{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Owns(&proxmoxv1alpha1.VirtualMachineSet{}).
		Owns(&proxmoxv1alpha1.VirtualMachine{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMSetmaxConcurrentReconciles}).
		Complete(&VirtualMachineSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}

func (r *VirtualMachineSetReconciler) CreateVirtualMachineCR(vmSet *proxmoxv1alpha1.VirtualMachineSet, index string) error {

	virtualMachine := &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vmSet.Name + "-" + index,
			Namespace: vmSet.Namespace,
			Labels:    labelsSetter(vmSet),
		},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name:     vmSet.Name + "-" + index,
			NodeName: vmSet.Spec.NodeName,
			Template: vmSet.Spec.Template,
		},
	}
	// Set VirtualMachineSet instance as the owner and controller
	if err := controllerutil.SetControllerReference(vmSet, virtualMachine, r.Scheme); err != nil {
		return err
	}
	// Create the VirtualMachine instance
	if err := r.Create(context.Background(), virtualMachine); err != nil {
		return err
	}
	return nil
}

func labelsSetter(vmSet *proxmoxv1alpha1.VirtualMachineSet) map[string]string {
	labels := make(map[string]string)
	labels["owner"] = vmSet.Name
	return labels
}

func (r *VirtualMachineSetReconciler) scaleUpVMs(vmSet *proxmoxv1alpha1.VirtualMachineSet, replicas int, vmList *proxmoxv1alpha1.VirtualMachineList) error {
	for i := len(vmList.Items); i < int(replicas); i++ {
		index := fmt.Sprintf("%d", i)
		if err := r.CreateVirtualMachineCR(vmSet, index); err != nil {
			return fmt.Errorf("unable to create VirtualMachine: %w", err)
		}
	}
	return nil
}

func (r *VirtualMachineSetReconciler) scaleDownVMs(ctx context.Context, replicas int, vmList *proxmoxv1alpha1.VirtualMachineList) error {
	for i := int(replicas); i < len(vmList.Items); i++ {
		vm := &vmList.Items[i]
		if err := r.Delete(ctx, vm); err != nil {
			return fmt.Errorf("unable to delete VirtualMachine object: %w", err)
		}
	}
	return nil
}

func (r *VirtualMachineSetReconciler) updateVMs(ctx context.Context, vmSet *proxmoxv1alpha1.VirtualMachineSet, vmList *proxmoxv1alpha1.VirtualMachineList) error {
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if !reflect.DeepEqual(vm.Spec.Template, vmSet.Spec.Template) {
			vm.Spec.Template = vmSet.Spec.Template
			if err := r.Update(ctx, vm); err != nil {
				return fmt.Errorf("unable to update VirtualMachine: %w", err)
			}
		}
	}
	return nil
}
