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

// "k8s.io/apimachinery/pkg/api/errors"
// "k8s.io/apimachinery/pkg/api/meta"
// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// "k8s.io/apimachinery/pkg/runtime"
// ctrl "sigs.k8s.io/controller-runtime"
// "sigs.k8s.io/controller-runtime/pkg/client"
// "sigs.k8s.io/controller-runtime/pkg/controller"
// "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
// "sigs.k8s.io/controller-runtime/pkg/event"
// "sigs.k8s.io/controller-runtime/pkg/log"
// "sigs.k8s.io/controller-runtime/pkg/predicate"

// proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
// "github.com/alperencelik/kubemox/pkg/kubernetes"
// "github.com/alperencelik/kubemox/pkg/proxmox"
// )

// // StorageDownloadURLReconciler reconciles a StorageDownloadURL object
// type StorageDownloadURLReconciler struct {
// client.Client
// Scheme *runtime.Scheme
// }

// const (
// storageDownloadURLFinalizerName = "storagedownloadurl.proxmox.alperen.cloud/finalizer"

// // Controller settings
// SDUreconcilationPeriod     = 10
// SDUmaxConcurrentReconciles = 3
// storageDownloadURLTimesNum = 5
// storageDownloadURLSteps    = 12

// // Status conditions
// typeAvailableStorageDownloadURL = "Available"
// typeCreatingStorageDownloadURL  = "Creating"
// typeDeletingStorageDownloadURL  = "Deleting"
// typeErrorStorageDownloadURL     = "Error"
// typeCompletedStorageDownloadURL = "Completed"
// )

// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls,verbs=get;list;watch;create;update;patch;delete
// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/status,verbs=get;update;patch
// //+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/finalizers,verbs=update

// // Reconcile is part of the main kubernetes reconciliation loop which aims to
// // move the current state of the cluster closer to the desired state.
// // TODO(user): Modify the Reconcile function to compare the state specified by
// // the StorageDownloadURL object against the actual cluster state, and then
// // perform operations to make the cluster state reflect the state specified by
// // the user.
// //
// // For more details, check Reconcile and its Result here:
// // - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
// func (r *StorageDownloadURLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
// logger := log.FromContext(ctx)

// // Fetch the StorageDownloadURL resource
// storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
// err := r.Get(ctx, req.NamespacedName, storageDownloadURL)
// if err != nil {
// return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
// }
// reconcileMode := kubernetes.GetReconcileMode(storageDownloadURL)

// switch reconcileMode {
// case kubernetes.ReconcileModeDisable:
// logger.Info("Reconciliation is disabled for the StorageDownloadURL")
// return ctrl.Result{}, nil
// default:
// break
// }

// logger.Info(fmt.Sprintf("Reconciling StorageDownloadURL %s", storageDownloadURL.Name))

// // Get fields from the spec
// node := storageDownloadURL.Spec.Node
// storage := storageDownloadURL.Spec.Storage

// if storageDownloadURL.ObjectMeta.DeletionTimestamp.IsZero() {
// if !controllerutil.ContainsFinalizer(storageDownloadURL, storageDownloadURLFinalizerName) {
// controllerutil.AddFinalizer(storageDownloadURL, storageDownloadURLFinalizerName)
// if err = r.Update(ctx, storageDownloadURL); err != nil {
// logger.Error(err, "unable to update StorageDownloadURL")
// return ctrl.Result{}, client.IgnoreNotFound(err)
// }
// }
// } else {
// if controllerutil.ContainsFinalizer(storageDownloadURL, storageDownloadURLFinalizerName) {
// // Delete the storage download URL
// res, delErr := r.handleDelete(ctx, storageDownloadURL)
// if delErr != nil {
// logger.Error(delErr, "unable to delete StorageDownloadURL")
// return res, delErr
// }
// if res.Requeue {
// return res, nil
// }
// }
// // Stop reconciliation as the object is being deleted
// return ctrl.Result{}, nil
// }

// // Check if the filename exists in the storage
// storageContent, err := proxmox.GetStorageContent(node, storage)
// if err != nil {
// logger.Error(err, "unable to get storage content")
// return ctrl.Result{}, err
// }
// // Check if the filename exists in the storage
// if !proxmox.HasFile(storageContent, &storageDownloadURL.Spec) {
// logger.Info("File does not exist in the storage, so downloading it")
// err = r.handleDownloadURL(ctx, storageDownloadURL)
// if err != nil {
// logger.Error(err, "unable to download the file")
// return ctrl.Result{}, err
// }
// } else {
// logger.Info("File exists in the storage")
// if !meta.IsStatusConditionPresentAndEqual(storageDownloadURL.Status.Conditions, typeAvailableStorageDownloadURL, metav1.ConditionTrue) {
// meta.SetStatusCondition(&storageDownloadURL.Status.Conditions, metav1.Condition{
// Type:   typeAvailableStorageDownloadURL,
// Status: metav1.ConditionTrue,
// Reason: "Available",
// Message: fmt.Sprintf("File %s exists in the storage %s",
// storageDownloadURL.Spec.Filename, storageDownloadURL.Spec.Storage),
// })
// storageDownloadURL.Status.Status = typeCompletedStorageDownloadURL
// if err := r.Status().Update(ctx, storageDownloadURL); err != nil {
// logger.Error(err, "unable to update StorageDownloadURL status")
// return ctrl.Result{}, err
// }
// }
// }
// return ctrl.Result{}, nil
// }

// // SetupWithManager sets up the controller with the Manager.
// func (r *StorageDownloadURLReconciler) SetupWithManager(mgr ctrl.Manager) error {
// return ctrl.NewControllerManagedBy(mgr).
// For(&proxmoxv1alpha1.StorageDownloadURL{}).
// WithEventFilter(predicate.Funcs{
// UpdateFunc: func(e event.UpdateEvent) bool {
// oldStorageDownloadURL := e.ObjectOld.(*proxmoxv1alpha1.StorageDownloadURL)
// newStorageDownloadURL := e.ObjectNew.(*proxmoxv1alpha1.StorageDownloadURL)
// condition1 := !reflect.DeepEqual(oldStorageDownloadURL.Spec, newStorageDownloadURL.Spec)
// condition2 := newStorageDownloadURL.ObjectMeta.GetDeletionTimestamp().IsZero()
// return condition1 || !condition2
// },
// }).
// Owns(&proxmoxv1alpha1.StorageDownloadURL{}).
// WithOptions(controller.Options{MaxConcurrentReconciles: SDUmaxConcurrentReconciles}).
// Complete(r)
// }

// func (r *StorageDownloadURLReconciler) handleResourceNotFound(ctx context.Context, err error) error {
// logger := log.FromContext(ctx)
// if errors.IsNotFound(err) {
// logger.Info("StorageDownloadURL resource not found. Ignoring since object must be deleted")
// return nil
// }
// logger.Error(err, "Failed to get StorageDownloadURL")
// return err
// }

// func (r *StorageDownloadURLReconciler) handleDownloadURL(ctx context.Context,
// storageDownloadURL *proxmoxv1alpha1.StorageDownloadURL) error {
// // Download the file
// logger := log.FromContext(ctx)

// taskUPID, taskErr := proxmox.StorageDownloadURL(storageDownloadURL.Spec.Node, &storageDownloadURL.Spec)
// if taskErr != nil {
// logger.Error(taskErr, "unable to download the file")
// return taskErr
// }
// // Get the task
// task := proxmox.GetTask(taskUPID)
// var logChannel <-chan string
// logChannel, err := task.Watch(ctx, 5)
// if err != nil {
// logger.Error(err, "unable to watch the task")
// return err
// }
// for logEntry := range logChannel {
// logger.Info(fmt.Sprintf("Download task for %s: %s", storageDownloadURL.Spec.Filename, logEntry))
// }
// _, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, storageDownloadURLTimesNum, storageDownloadURLSteps)
// if taskErr != nil {
// logger.Error(taskErr, "unable to get the task status")
// return taskErr
// }
// switch {
// case !taskCompleted:
// logger.Error(taskErr, "Download task did not complete")
// case taskCompleted:
// logger.Info("Download task completed")
// // Update the status
// meta.SetStatusCondition(&storageDownloadURL.Status.Conditions, metav1.Condition{
// Type:   typeAvailableStorageDownloadURL,
// Status: metav1.ConditionTrue,
// Reason: "Downloaded",
// Message: fmt.Sprintf("File %s has been downloaded successfully to %s storage",
// storageDownloadURL.Spec.Filename, storageDownloadURL.Spec.Storage),
// })
// storageDownloadURL.Status.Status = typeCompletedStorageDownloadURL
// if err := r.Status().Update(ctx, storageDownloadURL); err != nil {
// logger.Error(err, "unable to update StorageDownloadURL status")
// return err
// }
// default:
// logger.Info("Download task did not complete yet")
// }
// return nil
// }

// func (r *StorageDownloadURLReconciler) handleDelete(ctx context.Context,
// storageDownloadURL *proxmoxv1alpha1.StorageDownloadURL) (ctrl.Result, error) {
// logger := log.FromContext(ctx)
// var err error
// if !meta.IsStatusConditionPresentAndEqual(storageDownloadURL.Status.Conditions, typeDeletingStorageDownloadURL, metav1.ConditionTrue) {
// meta.SetStatusCondition(&storageDownloadURL.Status.Conditions, metav1.Condition{
// Type:    typeDeletingStorageDownloadURL,
// Status:  metav1.ConditionTrue,
// Reason:  "Deleting",
// Message: "Deleting StorageDownloadURL",
// })
// if err = r.Status().Update(ctx, storageDownloadURL); err != nil {
// logger.Error(err, "unable to update StorageDownloadURL status")
// return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
// }
// }
// // Delete the file from the storage
// err = proxmox.DeleteStorageContent(storageDownloadURL.Spec.Storage, &storageDownloadURL.Spec)
// if err != nil {
// logger.Error(err, "unable to delete the file")
// return ctrl.Result{}, client.IgnoreNotFound(err)
// }
// controllerutil.RemoveFinalizer(storageDownloadURL, storageDownloadURLFinalizerName)
// if err = r.Update(ctx, storageDownloadURL); err != nil {
// logger.Error(err, "unable to update StorageDownloadURL")
// return ctrl.Result{}, client.IgnoreNotFound(err)
// }
// return ctrl.Result{}, nil
// }
