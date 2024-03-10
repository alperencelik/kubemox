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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VirtualMachineReconciler reconciles a VirtualMachine object
type VirtualMachineStatusReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// Controller settings
	StatusReconcilationPeriod     = 30 // seconds
	StatusMaxConcurrentReconciles = 10 // number of concurrent reconciles
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachines/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *VirtualMachineStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Fetch the VirtualMachine instance's status
	vm := &proxmoxv1alpha1.VirtualMachine{}
	err := r.Get(ctx, req.NamespacedName, vm)
	if err != nil {
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// No need to output since it's ONLY tracking the status
	// logger.Info(fmt.Sprintf("Reconciling VirtualMachine status %s", vm.Name))

	if vm.Status.Conditions == nil || len(vm.Status.Conditions) == 0 {
		meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
			Type:    typeAvailableVirtualMachine,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		err = r.Status().Update(ctx, vm)
		if err != nil {
			logger.Error(err, "Error updating VirtualMachine status")
		}
	}

	// Re-fetch the VirtualMachine resource
	err = r.Get(ctx, req.NamespacedName, vm)
	if err != nil {
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status, err := proxmox.UpdateVMStatus(vm.Name, vm.Spec.NodeName)
	if err != nil {
		logger.Error(err, "unable to get status of VirtualMachine")
		return ctrl.Result{}, err
	}
	// Re-fetch the VirtualMachine resource
	if err = r.Get(ctx, req.NamespacedName, vm); err != nil {
		logger.Error(err, "unable to fetch VirtualMachine")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	vm.Status = *status
	err = r.Status().Update(ctx, vm)
	if err != nil {
		logger.Error(err, "unable to update VirtualMachine status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true, RequeueAfter: StatusReconcilationPeriod * time.Second}, client.IgnoreNotFound(err)
}

func (r *VirtualMachineStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Wait for the VirtualMachine to be created before calling Reconcile
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachine{}).
		Owns(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: StatusMaxConcurrentReconciles}).
		Complete(r)
}
