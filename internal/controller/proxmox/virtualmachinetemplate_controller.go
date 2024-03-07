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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// VirtualMachineTemplateReconciler reconciles a VirtualMachineTemplate object
type VirtualMachineTemplateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// Controller settings
	VirtualMachineTemplateFinalizer    = "virtualmachinetemplate.proxmox.alperen.cloud/finalizer"
	VirtualMachineConcurrentReconciles = 3
	VirtualMachineReconcilationPeriod  = 10
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinetemplates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinetemplates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinetemplates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VirtualMachineTemplate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *VirtualMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	virtualMachineTemplate := &proxmoxv1alpha1.VirtualMachineTemplate{}
	err := r.Get(ctx, req.NamespacedName, virtualMachineTemplate)
	if err != nil {
		logger.Error(err, "unable to fetch VirtualMachineTemplate")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the VirtualMachineTemplate is marked for deletion
	if virtualMachineTemplate.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(virtualMachineTemplate, VirtualMachineTemplateFinalizer) {
			controllerutil.AddFinalizer(virtualMachineTemplate, VirtualMachineTemplateFinalizer)
			if err := r.Update(ctx, virtualMachineTemplate); err != nil {
				logger.Error(err, "unable to update VirtualMachineTemplate with finalizer")
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(virtualMachineTemplate, VirtualMachineTemplateFinalizer) {
			// Custom deletion logic goes here
		}
		// Remove the finalizer
		controllerutil.RemoveFinalizer(virtualMachineTemplate, VirtualMachineTemplateFinalizer)
		if err := r.Update(ctx, virtualMachineTemplate); err != nil {
			logger.Error(err, "unable to update VirtualMachineTemplate without finalizer")
			return ctrl.Result{}, err
		}

		// Return if the VirtualMachineTemplate is marked for deletion
		return ctrl.Result{}, client.IgnoreNotFound(nil)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: VirtualMachineReconcilationPeriod * time.Second}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineTemplate{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VirtualMachineConcurrentReconciles}).
		Complete(r)
}
