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

// import (
// "context"
// "fmt"
// "reflect"
// "time"

// "k8s.io/apimachinery/pkg/api/errors"
// "k8s.io/apimachinery/pkg/runtime"
// ctrl "sigs.k8s.io/controller-runtime"
// "sigs.k8s.io/controller-runtime/pkg/client"
// "sigs.k8s.io/controller-runtime/pkg/controller"
// "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
// "sigs.k8s.io/controller-runtime/pkg/event"
// "sigs.k8s.io/controller-runtime/pkg/log"
// "sigs.k8s.io/controller-runtime/pkg/predicate"

// proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
// "github.com/alperencelik/kubemox/pkg/proxmox"
// "github.com/alperencelik/kubemox/pkg/utils"
// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// )

// // VirtualMachineSnapshotReconciler reconciles a VirtualMachineSnapshot object
// type VirtualMachineSnapshotReconciler struct {
// client.Client
// Scheme *runtime.Scheme
// }

// var (
// StatusCode int
// )

// const (
// // Controller settings
// VMSnapshotreconcilationPeriod     = 10
// VMSnapshotmaxConcurrentReconciles = 3
// snapshotCreatedStatus             = "Created"
// )

// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots,verbs=get;list;watch;create;update;patch;delete
// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots/status,verbs=get;update;patch
// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinesnapshots/finalizers,verbs=update

// // Reconcile is part of the main kubernetes reconciliation loop which aims to
// // move the current state of the cluster closer to the desired state.
// // TODO(user): Modify the Reconcile function to compare the state specified by
// // the VirtualMachineSnapshot object against the actual cluster state, and then
// // perform operations to make the cluster state reflect the state specified by
// // the user.
// //
// // For more details, check Reconcile and its Result here:
// // - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
// func (r *VirtualMachineSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
// logger := log.FromContext(ctx)

// vmSnapshot := &proxmoxv1alpha1.VirtualMachineSnapshot{}
// err := r.Get(ctx, req.NamespacedName, vmSnapshot)
// if err != nil {
// return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
// }
// logger.Info("Reconciling VirtualMachineSnapshot", "Name", vmSnapshot.Name)

// // Get ref vm
// vm, err := r.getVMreference(ctx, vmSnapshot)
// if err != nil {
// return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
// }

// // Set ownerRef for the VirtualMachineSnapshot
// if err = controllerutil.SetControllerReference(vm, vmSnapshot, r.Scheme); err != nil {
// logger.Error(err, "unable to set owner reference for VirtualMachineSnapshot")
// return ctrl.Result{}, err
// }
// snapshotName := vmSnapshot.Spec.SnapshotName
// if snapshotName == "" {
// // If snapshot name is not specified, use the timestamp as the snapshot name
// snapshotName = fmt.Sprintf("snapshot_%s", time.Now().Format("2006_01_02T15_04_05Z07_00"))
// }

// // Handle snapshot creation
// err = r.handleSnapshotCreation(ctx, vmSnapshot, snapshotName)
// if err != nil {
// return ctrl.Result{Requeue: true, RequeueAfter: VMSnapshotreconcilationPeriod}, client.IgnoreNotFound(err)
// }

// return ctrl.Result{}, client.IgnoreNotFound(err)
// }

// // SetupWithManager sets up the controller with the Manager.
// func (r *VirtualMachineSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
// return ctrl.NewControllerManagedBy(mgr).
// For(&proxmoxv1alpha1.VirtualMachineSnapshot{}).
// WithEventFilter(predicate.Funcs{
// UpdateFunc: func(e event.UpdateEvent) bool {
// oldSnapshot := e.ObjectOld.(*proxmoxv1alpha1.VirtualMachineSnapshot)
// newSnapshot := e.ObjectNew.(*proxmoxv1alpha1.VirtualMachineSnapshot)
// condition1 := !reflect.DeepEqual(oldSnapshot.Spec, newSnapshot.Spec)
// return condition1
// },
// }).
// WithOptions(controller.Options{MaxConcurrentReconciles: VMSnapshotmaxConcurrentReconciles}).
// Complete(r)
// }

// func (r *VirtualMachineSnapshotReconciler) handleResourceNotFound(ctx context.Context, err error) error {
// logger := log.FromContext(ctx)
// if errors.IsNotFound(err) {
// logger.Info("VirtualMachineSnapshot resource not found. Ignoring since object must be deleted")
// return nil
// }
// logger.Error(err, "Failed to get VirtualMachineSnapshot")
// return err
// }

// func (r *VirtualMachineSnapshotReconciler) getVMreference(ctx context.Context,
// vmSnapshot *proxmoxv1alpha1.VirtualMachineSnapshot) (*proxmoxv1alpha1.VirtualMachine, error) {
// logger := log.FromContext(ctx)
// vmName := vmSnapshot.Spec.VirtualMachineName

// vm := &proxmoxv1alpha1.VirtualMachine{}
// // VirtualMachine is now Cluster scoped
// err := r.Get(ctx, client.ObjectKey{Name: vmName}, vm)
// if err != nil {
// if errors.IsNotFound(err) {
// logger.Error(err, "VirtualMachine not found. Ignoring since object must be deleted")
// return nil, err
// }
// logger.Error(err, "Failed to get VirtualMachine")
// return nil, err
// }
// return vm, nil
// }

// func (r *VirtualMachineSnapshotReconciler) handleSnapshotCreation(ctx context.Context,
// vmSnapshot *proxmoxv1alpha1.VirtualMachineSnapshot, snapshotName string) error {
// logger := log.FromContext(ctx)
// vmName := vmSnapshot.Spec.VirtualMachineName
// // Get all the snapshots of the VM
// snapshots, err := proxmox.GetVMSnapshots(vmName)
// if err != nil {
// logger.Error(err, "Failed to get VM snapshots")
// return err
// }
// // If snapshotName is exists in the snapshots, return
// if utils.ExistsIn([]string{snapshotName}, snapshots) {
// logger.Info("Snapshot is already exists")
// vmSnapshot.Status.Status = snapshotCreatedStatus
// err = r.Status().Update(ctx, vmSnapshot)
// if err != nil {
// return client.IgnoreNotFound(err)
// }
// return nil
// } else if vmSnapshot.Status.Status != snapshotCreatedStatus {
// // Create the snapshot
// StatusCode, err = proxmox.CreateVMSnapshot(vmName, snapshotName)
// if err != nil {
// logger.Error(err, "Failed to create VM snapshot")
// return client.IgnoreNotFound(err)
// }
// switch StatusCode {
// case 0:
// vmSnapshot.Status.Status = snapshotCreatedStatus
// vmSnapshot.Spec.Timestamp = metav1.Now()
// if err = r.Status().Update(ctx, vmSnapshot); err != nil {
// return client.IgnoreNotFound(err)
// }
// logger.Info(fmt.Sprintf("Snapshot %s is created for %s", snapshotName, vmName))
// default:
// // Snapshot creation failed, return
// vmSnapshot.Status.ErrorMessage = "Snapshot creation failed"
// if err = r.Status().Update(ctx, vmSnapshot); err != nil {
// return client.IgnoreNotFound(err)
// }
// }
// }
// return nil
// }
