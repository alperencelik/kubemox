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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// StorageDownloadURLReconciler reconciles a StorageDownloadURL object
type StorageDownloadURLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	storageDownloadURLFinalizerName = "storagedownloadurl.proxmox.alperen.cloud/finalizer"

	// Controller settings
	SDUreconcilationPeriod     = 10
	SDUmaxConcurrentReconciles = 3
	storageDownloadURLTimesNum = 5
	storageDownloadURLSteps    = 12
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=storagedownloadurls/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the StorageDownloadURL object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *StorageDownloadURLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	Log := log.FromContext(ctx)

	// Fetch the StorageDownloadURL resource
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, req.NamespacedName, storageDownloadURL)
	if err != nil {
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}
	// Get fields from the spec
	content := storageDownloadURL.Spec.Content
	node := storageDownloadURL.Spec.Node
	storage := storageDownloadURL.Spec.Storage
	// Check if the content is only iso or vztmpl
	if content != "iso" && content != "vztmpl" {
		Log.Info("Content should be either iso or vztmpl")
		return ctrl.Result{}, nil
	}
	if storageDownloadURL.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(storageDownloadURL, storageDownloadURLFinalizerName) {
			controllerutil.AddFinalizer(storageDownloadURL, storageDownloadURLFinalizerName)
			if err := r.Update(ctx, storageDownloadURL); err != nil {
				log.Log.Error(err, "unable to update StorageDownloadURL")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(storageDownloadURL, storageDownloadURLFinalizerName) {
			deletionKey := fmt.Sprintf("%s/%s-deletion", storageDownloadURL.Namespace, storageDownloadURL.Name)
			if isProcessed(deletionKey) {
			} else {
				// Delete the file from the storage
				// TODO implement the deletion
				processedResources[deletionKey] = true
			}
			controllerutil.RemoveFinalizer(storageDownloadURL, storageDownloadURLFinalizerName)
			if err := r.Update(ctx, storageDownloadURL); err != nil {
				log.Log.Error(err, "unable to update StorageDownloadURL")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		// Stop reconciliation as the object is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the filename exists in the storage
	storageContent, err := proxmox.GetStorageContent(node, storage)
	if err != nil {
		Log.Error(err, "unable to get storage content")
		return ctrl.Result{}, err
	}
	// Check if the filename exists in the storage
	if !proxmox.HasFile(storageContent, &storageDownloadURL.Spec) {
		resourceKey := fmt.Sprintf("%s:%s/%s", storage, content, storageDownloadURL.Spec.Filename)
		if isProcessed(resourceKey) {
		} else {
			Log.Info("File does not exist in the storage, so downloading it")
			// Download the file
			taskUPID, err := proxmox.StorageDownloadURL(node, &storageDownloadURL.Spec)
			if err != nil {
				Log.Error(err, "unable to download the file")
				return ctrl.Result{}, err
			}
			// Get the task
			task := proxmox.GetTask(taskUPID)
			logChannel, err := task.Watch(ctx, 5)
			if err != nil {
				Log.Error(err, "unable to watch the task")
				return ctrl.Result{}, err
			}
			for logEntry := range logChannel {
				log.Log.Info(fmt.Sprintf("Download task for %s: %s", storageDownloadURL.Spec.Filename, logEntry))
			}
			_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, storageDownloadURLTimesNum, storageDownloadURLSteps)
			if taskErr != nil {
				Log.Error(taskErr, "unable to get the task status")
				return ctrl.Result{}, taskErr
			}
			switch {
			case !taskCompleted:
				log.Log.Error(taskErr, "Download task did not complete")
			case taskCompleted:
				Log.Info("Download task completed")
				processedResources[resourceKey] = true
			default:
				Log.Info("Download task did not complete yet")
			}

		}
	}
	return ctrl.Result{Requeue: true, RequeueAfter: SDUreconcilationPeriod * time.Second}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *StorageDownloadURLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.StorageDownloadURL{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: SDUmaxConcurrentReconciles}).
		Complete(&StorageDownloadURLReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}
