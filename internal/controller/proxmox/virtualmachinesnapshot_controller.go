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
	"time"

	"github.com/alperencelik/kubemox/pkg/proxmox"
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

// VirtualMachineSnapshotReconciler reconciles a VirtualMachineSnapshot object
type VirtualMachineSnapshotReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

var (
	StatusCode int
)

const (
	// Controller settings
	VMSnapshotreconcilationPeriod     = 10
	VMSnapshotmaxConcurrentReconciles = 3
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VirtualMachineSnapshot object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *VirtualMachineSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)

	// TODO(user): your logic here
	vmSnapshot := &proxmoxv1alpha1.VirtualMachineSnapshot{}
	err := r.Get(ctx, req.NamespacedName, vmSnapshot)
	if err != nil {
		Log.Error(err, "unable to fetch VirtualMachineSnapshot")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	vmName := vmSnapshot.Spec.VirtualMachineName
	snapshotName := vmSnapshot.Spec.SnapshotName
	vm := &proxmoxv1alpha1.VirtualMachine{}
	err = r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: vmName}, vm)
	if err != nil {
		Log.Error(err, "unable to fetch VirtualMachine")
	}
	// Set owner reference for the VirtualMachine
	//	ownerRef := metav1.OwnerReference{
	//		APIVersion: vm.APIVersion,
	//		Kind:       vm.Kind,
	//		Name:       vmName,
	//		UID:        vm.ObjectMeta.UID,
	//	}
	// Set ownerRef for the VirtualMachineSnapshot
	if err := controllerutil.SetControllerReference(vm, vmSnapshot, r.Scheme); err != nil {
		Log.Error(err, "unable to set owner reference for VirtualMachineSnapshot")
		return ctrl.Result{}, err
	}

	if snapshotName == "" {
		// If snapshot name is not specified, use the timestamp as the snapshot name
		snapshotName = fmt.Sprintf("snapshot_%s", time.Now().Format("2006_01_02T15_04_05Z07_00"))
	}
	// If the snapshot is already created, return
	if vmSnapshot.Status.Status == "" {
		// Create the snapshot
		StatusCode = proxmox.CreateVMSnapshot(vmName, snapshotName)
		// Set the status to created
		if StatusCode == 0 {
			vmSnapshot.Spec.Timestamp = metav1.Now()
			err = r.Update(ctx, vmSnapshot)
			if err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			vmSnapshot.Status.Status = "Created"
			err = r.Status().Update(ctx, vmSnapshot)
			if err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else if vmSnapshot.Status.Status == "Created" {
		if StatusCode == 2 {
			// Snapshot is already created, return
			vmSnapshot.Status.ErrorMessage = "Snapshot is already created"
			err := r.Status().Update(ctx, vmSnapshot)
			if err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	} else {
		// Snapshot creation failed, return
		if StatusCode == 1 {
			vmSnapshot.Status.ErrorMessage = "Snapshot creation failed"
			err = r.Status().Update(ctx, vmSnapshot)
			if err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{Requeue: true, RequeueAfter: VMSnapshotreconcilationPeriod * time.Second}, client.IgnoreNotFound(r.Get(ctx, req.NamespacedName, &proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}))
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSnapshot{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMSnapshotmaxConcurrentReconciles}).
		Complete(&VirtualMachineSnapshotReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}
