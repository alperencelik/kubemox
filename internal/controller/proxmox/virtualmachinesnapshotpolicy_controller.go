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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
	logger := log.FromContext(ctx)

	// TODO(user): your logic here
	vmSnapshotPolicy := &proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}
	err := r.Get(ctx, req.NamespacedName, vmSnapshotPolicy)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	// TODO: Add deletion logic for snapshots

	// Start snapshot cron jobs
	if err := r.StartSnapshotCronJobs(ctx, vmSnapshotPolicy); err != nil {
		logger.Error(err, "unable to start snapshot cron jobs")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSnapshotPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSnapshotPolicy{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMSnapshotPolicymaxConcurrentReconciles}).
		Complete(r)
}

func GetMatchingVirtualMachines(vmSnapshotPolicy *proxmoxv1alpha1.VirtualMachineSnapshotPolicy,
	r *VirtualMachineSnapshotPolicyReconciler, ctx context.Context) []proxmoxv1alpha1.VirtualMachine {
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
	return MatchingVirtualMachines
}

func VMSnapshotCR(vmName, snapshotName, namespace, connectionName string) *proxmoxv1alpha1.VirtualMachineSnapshot {
	return &proxmoxv1alpha1.VirtualMachineSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", vmName, time.Now().Format("2006-01-2t15-040-5z07-00")),
			Namespace: namespace,
		},
		Spec: proxmoxv1alpha1.VirtualMachineSnapshotSpec{
			VirtualMachineName: vmName,
			SnapshotName:       snapshotName,
			ConnectionRef: &corev1.LocalObjectReference{
				Name: connectionName,
			},
		},
	}
}

func (r *VirtualMachineSnapshotPolicyReconciler) StartSnapshotCronJobs(ctx context.Context,
	vmSnapshotPolicy *proxmoxv1alpha1.VirtualMachineSnapshotPolicy) error {
	logger := log.FromContext(ctx)
	cronSpec := vmSnapshotPolicy.Spec.SnapshotSchedule
	// Get matching VirtualMachines
	MatchingVirtualMachines := GetMatchingVirtualMachines(vmSnapshotPolicy, r, ctx)
	// Get connection name
	connectionName := vmSnapshotPolicy.Spec.ConnectionRef.Name

	// Iterate over matching VirtualMachines to create snapshot
	c := cron.New()
	if _, err := c.AddFunc(cronSpec, func() {
		for i := range MatchingVirtualMachines {
			vm := &MatchingVirtualMachines[i]
			// Create snapshot
			vmName := vm.Spec.Name
			snapshotName := fmt.Sprintf("snapshot_%s", time.Now().Format("2006_01_02T15_04_05"))
			namespace := vm.Namespace
			// Create VirtualMachineSnapshot object
			vmSnapshot := VMSnapshotCR(vmName, snapshotName, namespace, connectionName)
			// Get VirtualMachineSnapshot object and if it does not exist, create it
			// If it exists, do nothing
			foundvmSnapshot := &proxmoxv1alpha1.VirtualMachineSnapshot{}
			err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: vmSnapshot.Name}, foundvmSnapshot)
			if err != nil {
				if errors.IsNotFound(err) {
					// Create the VirtualMachineSnapshot
					logger.Info("Creating a new VirtualMachineSnapshot", "VirtualMachineSnapshot.Namespace",
						vmSnapshot.Namespace, "VirtualMachineSnapshot.Name", vmSnapshot.Name)
					if err := r.Create(ctx, vmSnapshot); err != nil {
						logger.Error(err, "unable to create VirtualMachineSnapshot")
						return
					}
				}
			}
		}
	}); err != nil {
		log.Log.Error(err, "unable to add cronjob")
		return err
	}
	c.Start()

	return nil
}

func (r *VirtualMachineSnapshotPolicyReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("VirtualMachineSnapshotPolicy resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachineSnapshotPolicy")
	return err
}
