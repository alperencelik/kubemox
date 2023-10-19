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

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VirtualMachineSnapshotPolicyReconciler reconciles a VirtualMachineSnapshotPolicy object
type VirtualMachineSnapshotPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	// Controller settings
	VMSnapshotPolicyreconcilationPeriod     = 10
	VMSnapshotPolicymaxConcurrentReconciles = 3
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshotpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshotpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshotpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VirtualMachineSnapshotPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *VirtualMachineSnapshotPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)

	// TODO(user): your logic here
	vmSnapshotPolicy := &proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}
	if err := r.Get(ctx, req.NamespacedName, vmSnapshotPolicy); err != nil {
		Log.Error(err, "unable to fetch VirtualMachineSnapshotPolicy")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	cronSpec := vmSnapshotPolicy.Spec.SnapshotSchedule
	// delete later
	matchingNamepaces := vmSnapshotPolicy.Spec.NamespaceSelector
	matchingLabels := vmSnapshotPolicy.Spec.LabelSelector.MatchLabels
	// Find matching VirtualMachines on matching namespaces
	var MatchingVirtualMachines []proxmoxv1alpha1.VirtualMachine
	for _, namespace := range matchingNamepaces.Namespaces {
		// Append to vmList
		virtualMachineList := &proxmoxv1alpha1.VirtualMachineList{}
		if err := r.List(ctx, virtualMachineList, client.InNamespace(namespace), client.MatchingLabels(matchingLabels)); err != nil {
			log.Log.Error(err, "unable to list VirtualMachines")
		}
		MatchingVirtualMachines = append(MatchingVirtualMachines, virtualMachineList.Items...)
	}
	// Print length of vmList

	// Iterate over matching VirtualMachines to create snapshot
	c := cron.New()
	if _, err := c.AddFunc(cronSpec, func() {
		for _, vm := range MatchingVirtualMachines {
			// Create snapshot
			vmName := vm.Spec.Name
			snapshotName := fmt.Sprintf("snapshot_%s", time.Now().Format("2006_01_02T15_04_05Z07_00"))
			// Create VirtualMachineSnapshot object
			vmSnapshot := &proxmoxv1alpha1.VirtualMachineSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-%s", vmName, time.Now().Format("2006-01-2t15-040-5z07-00")),
					Namespace: vmSnapshotPolicy.Namespace,
				},
				Spec: proxmoxv1alpha1.VirtualMachineSnapshotSpec{
					VirtualMachineName: vmName,
					SnapshotName:       snapshotName,
				},
			}
			// If vmSnapshot doesn't exist, create it if it does, return
			snapshotKey := fmt.Sprintf("%s/%s", vmSnapshot.Namespace, vmSnapshot.Name)
			if isProcessed(snapshotKey) {
				return // already processed
			} else {
				if err := r.Create(ctx, vmSnapshot); err != nil {
					return // requeue
				}
				processedResources[snapshotKey] = true
			}
		}

	}); err != nil {
		log.Log.Error(err, "unable to add cronjob")
		return ctrl.Result{}, err
	}

	c.Start()
	// Create snapshot

	return ctrl.Result{Requeue: true, RequeueAfter: VMSnapshotPolicyreconcilationPeriod * time.Second}, client.IgnoreNotFound(r.Get(ctx, req.NamespacedName, &proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}))
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSnapshotPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMSnapshotPolicymaxConcurrentReconciles}).
		Complete(&VirtualMachineSnapshotPolicyReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})

}
