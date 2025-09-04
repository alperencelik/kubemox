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

	"k8s.io/apimachinery/pkg/api/errors"
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

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesets/finalizers,verbs=update

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
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}

	reconcileMode := kubernetes.GetReconcileMode(vmSet)

	switch reconcileMode {
	case kubernetes.ReconcileModeDisable:
		// Disable the reconciliation
		logger.Info(fmt.Sprintf("Reconciliation is disabled for VirtualMachineSet %s", vmSet.Name))
		return ctrl.Result{}, nil
	default:
		// Normal mode
		break
	}

	logger.Info(fmt.Sprintf("Reconciling VirtualMachineSet %s", vmSet.Name))

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

	// DELETE
	if vmSet.DeletionTimestamp.IsZero() {
		err = r.handleFinalizer(ctx, vmSet)
		if err != nil {
			logger.Error(err, "unable to handle finalizer")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
			// Ensure that the pre-delete logic is idempotent.
			logger.Info(fmt.Sprintf("Deleting VirtualMachineSet %s", vmSet.Name))
			res, delErr := r.handleDelete(ctx, vmSet, vmList)
			if delErr != nil {
				logger.Error(delErr, "unable to delete VirtualMachineSet")
				return res, delErr
			}
			if res != (ctrl.Result{}) {
				return res, nil
			}
		}
		// Requeue the request until the vmSet has no VirtualMachines
		return ctrl.Result{Requeue: true, RequeueAfter: VMSetreconcilationPeriod * time.Second}, client.IgnoreNotFound(err)
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

	// If the number of the VirtualMachines is less than the desired number of replicas and the object
	// is not being deleted, create the VirtualMachines
	err = r.handleVMsetOperations(ctx, vmSet, vmList)
	if err != nil {
		logger.Error(err, "unable to handle VirtualMachineSet operations")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineSet{}).
		Owns(&proxmoxv1alpha1.VirtualMachine{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMSetmaxConcurrentReconciles}).
		Complete(&VirtualMachineSetReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}

func (r *VirtualMachineSetReconciler) createOrUpdateVirtualMachineCRs(vmSet *proxmoxv1alpha1.VirtualMachineSet) error {
	for i := 0; i < vmSet.Spec.Replicas; i++ {
		index := strconv.Itoa(i)
		// Define a new VirtualMachine object
		virtualMachine := &proxmoxv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vmSet.Name + "-" + index,
				Namespace: vmSet.Namespace,
			},
		}

		// Create or update the VirtualMachine instance
		_, err := controllerutil.CreateOrUpdate(context.Background(), r.Client, virtualMachine, func() error {
			// Set the owner and controller reference
			if err := controllerutil.SetControllerReference(vmSet, virtualMachine, r.Scheme); err != nil {
				return err
			}

			// Set the labels
			virtualMachine.Labels = labelsSetter(vmSet)

			// Deep copy the template from the vmSet
			template := vmSet.Spec.Template.DeepCopy()

			// Assign PCI devices
			if vmSet.Spec.PCIDevices != nil {
				switch vmSet.Spec.PCIDevices.Mode {
				case "shared":
					template.PciDevices = vmSet.Spec.PCIDevices.Devices
				case "dedicated":
					if i < len(vmSet.Spec.PCIDevices.Devices) {
						template.PciDevices = []proxmoxv1alpha1.PciDevice{vmSet.Spec.PCIDevices.Devices[i]}
					} else {
						template.PciDevices = nil
					}
				}
			}

			// Set the spec
			virtualMachine.Spec = proxmoxv1alpha1.VirtualMachineSpec{
				Name:               vmSet.Name + "-" + index,
				NodeName:           vmSet.Spec.NodeName,
				Template:           template,
				DeletionProtection: vmSet.Spec.DeletionProtection,
				EnableAutoStart:    vmSet.Spec.EnableAutoStart,
				AdditionalConfig:   vmSet.Spec.AdditionalConfig,
				ConnectionRef:      vmSet.Spec.ConnectionRef,
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func labelsSetter(vmSet *proxmoxv1alpha1.VirtualMachineSet) map[string]string {
	labels := make(map[string]string)
	labels["owner"] = vmSet.Name
	return labels
}

func (r *VirtualMachineSetReconciler) scaleDownVMs(vmSet *proxmoxv1alpha1.VirtualMachineSet,
	vmList *proxmoxv1alpha1.VirtualMachineList) error {
	// Create a map of expected VirtualMachines
	expectedVMMap := make(map[string]bool)
	for i := 0; i < vmSet.Spec.Replicas; i++ {
		vmName := fmt.Sprintf("%s-%d", vmSet.Name, i)
		expectedVMMap[vmName] = true
	}
	// Delete any VirtualMachines that are not in the expectedVmMap
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if _, exists := expectedVMMap[vm.Name]; !exists {
			if err := r.Delete(context.Background(), vm); err != nil {
				return fmt.Errorf("unable to delete VirtualMachine: %w", err)
			}
		}
	}
	return nil
}

func (r *VirtualMachineSetReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("VirtualMachineSet resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachineSet")
	return err
}

func (r *VirtualMachineSetReconciler) handleVMsetOperations(ctx context.Context, vmSet *proxmoxv1alpha1.VirtualMachineSet,
	vmList *proxmoxv1alpha1.VirtualMachineList) error {
	logger := log.FromContext(ctx)
	replicas := vmSet.Spec.Replicas

	// If the number of the VirtualMachines is more than the desired number of replicas
	if len(vmList.Items) > replicas {
		if err := r.scaleDownVMs(vmSet, vmList); err != nil {
			logger.Error(err, "unable to scale down VirtualMachines")
			return err
		}
		// Set the condition for the VirtualMachineSet
		meta.SetStatusCondition(&vmSet.Status.Conditions, metav1.Condition{
			Type:    typeScalingDownVirtualMachineSet,
			Status:  metav1.ConditionTrue,
			Reason:  "ScaledDown",
			Message: "VirtualMachines scaled down",
		})
		if err := r.Status().Update(ctx, vmSet); err != nil {
			logger.Error(err, "Error updating VirtualMachineSet status")
			return err
		}
	} else {
		if err := r.createOrUpdateVirtualMachineCRs(vmSet); err != nil {
			return err
		}
	}
	return nil
}

func (r *VirtualMachineSetReconciler) handleFinalizer(ctx context.Context, vmSet *proxmoxv1alpha1.VirtualMachineSet) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(vmSet, virtualMachineSetFinalizerName) {
		controllerutil.AddFinalizer(vmSet, virtualMachineSetFinalizerName)
		if err := r.Update(ctx, vmSet); err != nil {
			logger.Error(err, "Error updating VirtualMachineSet")
			return err
		}
	}
	return nil
}

func (r *VirtualMachineSetReconciler) handleDelete(ctx context.Context, vmSet *proxmoxv1alpha1.VirtualMachineSet,
	vmList *proxmoxv1alpha1.VirtualMachineList) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var err error

	if !meta.IsStatusConditionPresentAndEqual(vmSet.Status.Conditions, typeDeletingVirtualMachineSet, metav1.ConditionUnknown) {
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
	}
	// Get VM list and delete them
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		if err = r.Delete(ctx, vm); err != nil {
			logger.Error(err, "unable to delete VirtualMachine")
			return ctrl.Result{RequeueAfter: 1 * time.Second}, client.IgnoreNotFound(err)
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
		return ctrl.Result{RequeueAfter: 1 * time.Second}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}
