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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// VirtualMachineTemplateReconciler reconciles a VirtualMachineTemplate object
type VirtualMachineTemplateReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Watchers *proxmox.ExternalWatchers
	Recorder record.EventRecorder
}

const (
	virtualMachineTemplateFinalizerName = "virtualmachinetemplate.proxmox.alperen.cloud/finalizer"

	// Controller settings
	VMTemplateMaxConcurrentReconciles = 3

	// Status conditions
	typeAvailableVirtualMachineTemplate = "Available"
	typeCreatingVirtualMachineTemplate  = "Creating"
	typeDeletingVirtualMachineTemplate  = "Deleting"
	typeErrorVirtualMachineTemplate     = "Error"
)

// Perform all operations to create the VirtualMachineTemplate. The logic should have three steps:
// 1. Check if the image is exist in the Proxmox node
// 2. If not, create and StorageDownloadURL object to download image and wait for the download to complete
// 3. Create the VM template with the given configuration

var imageNameFormat string = "%s-image"

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
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.3/pkg/reconcile
func (r *VirtualMachineTemplateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Get the VirtualMachineTemplate object with this namespace/name
	vmTemplate := &proxmoxv1alpha1.VirtualMachineTemplate{}
	if err := r.Get(ctx, req.NamespacedName, vmTemplate); err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	logger.Info(fmt.Sprintf("Reconciling VirtualMachineTemplate %s", vmTemplate.Name))

	// Handle the external watcher for the VirtualMachineTemplate
	r.handleWatcher(ctx, req, vmTemplate)

	// Check if the VirtualMachineTemplate is marked for deleted, which is indicated by the deletion timestamp being set.
	if vmTemplate.ObjectMeta.DeletionTimestamp.IsZero() {
		err := r.handleFinalizer(ctx, vmTemplate)
		if err != nil {
			logger.Error(err, "Failed to handle finalizer")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(vmTemplate, virtualMachineTemplateFinalizerName) {
			// Update the condition for the VirtualMachineTemplate if it's not already deleting
			res, delErr := r.handleDelete(ctx, req, vmTemplate)
			if delErr != nil {
				logger.Error(delErr, "Failed to delete VirtualMachineTemplate")
				return res, delErr
			}
			if res.Requeue {
				return res, nil
			}
		}
		// Stop the reconciliation as the object is being deleted
		return ctrl.Result{}, nil
	}

	// Handle the image operations and make sure the image is available
	err := r.handleImageOperations(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}

	// Handle the StorageDownloadURL object
	result, err := r.handleStorageDownloadURL(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	if result.Requeue {
		return result, nil
	}
	// Create the VM template
	err = r.handleVMCreation(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	// Do the CloudInit operations
	err = r.handleCloudInitOperations(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	// Update the condition for the VirtualMachineTemplate if it's not already ready
	result, err = r.handleStatus(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineTemplate{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldVMTemplate := e.ObjectOld.(*proxmoxv1alpha1.VirtualMachineTemplate)
				newVMTemplate := e.ObjectNew.(*proxmoxv1alpha1.VirtualMachineTemplate)
				condition1 := !reflect.DeepEqual(oldVMTemplate.Spec, newVMTemplate.Spec)
				condition2 := newVMTemplate.ObjectMeta.GetDeletionTimestamp().IsZero()
				return condition1 || !condition2
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: VMTemplateMaxConcurrentReconciles}).
		Complete(r)
}

func (r *VirtualMachineTemplateReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("VirtualMachineTemplate resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get VirtualMachineTemplate")
	return err
}

func (r *VirtualMachineTemplateReconciler) handleImageOperations(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	// Create a StorageDownloadURL object
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, client.ObjectKey{Name: fmt.Sprintf(imageNameFormat, vmTemplate.Name),
		Namespace: vmTemplate.Namespace}, storageDownloadURL)
	if err != nil {
		if errors.IsNotFound(err) {
			// If storageDownloadURL is not found, create a new one
			err = r.createStorageDownloadURLCR(ctx, vmTemplate)
			if err != nil {
				logger.Error(err, "Failed to create StorageDownloadURL")
				return err
			}
		} else {
			// Some other error occurred
			return r.handleResourceNotFound(ctx, err)
		}
	}
	return nil
}

// Return true if the request should be requeued
func (r *VirtualMachineTemplateReconciler) handleVMCreation(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	// Check if the VM is already exists and
	templateVMName := vmTemplate.Spec.Name
	nodeName := vmTemplate.Spec.NodeName

	// Check if the VM template is already exists
	vmExists, err := proxmox.CheckVM(templateVMName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to check VM template")
		return err
	}
	if vmExists {
		// Check if VM template is on desired state
		if result, err := proxmox.CheckVirtualMachineTemplateDelta(vmTemplate); err == nil && result {
			// Update VirtualMachineTemplate resource
			err = proxmox.UpdateVirtualMachineTemplate(vmTemplate)
			if err != nil {
				logger.Error(err, "Failed to update VM template")
				return err
			}
		} else {
			// VM template is already exists and on desired state
			logger.Info(fmt.Sprintf("VirtualMachine template %s already exists", templateVMName))
			return nil
		}
	} else {
		// Even if the VM is exists, it may not be a template but we will create a new one no matter what
		logger.Info(fmt.Sprintf("VirtualMachine template %s does not exists, creating a new one", templateVMName))
		// Create the VM first
		task, err := proxmox.CreateVMTemplate(vmTemplate)
		if err != nil {
			logger.Error(err, "Failed to create VM for template")
			return err
		}
		// Wait for the task to complete
		_, taskCompleted, err := task.WaitForCompleteStatus(ctx, 3, 7)
		if !taskCompleted {
			logger.Error(err, "Failed to create VM for template")
			return err
		}
		// Add tag to the VM
		task, err = proxmox.AddTagToVMTemplate(vmTemplate)
		if err != nil {
			logger.Error(err, "Failed to add tag to VM template")
		}
		// Wait for the task to complete
		_, taskCompleted, err = task.WaitForCompleteStatus(ctx, 3, 7)
		if !taskCompleted {
			logger.Error(err, "Failed to add tag to VM template")
			return err
		}
		r.Recorder.Event(vmTemplate, "Normal", "VMTemplateCreated", fmt.Sprintf("VirtualMachine template %s is created", templateVMName))
	}
	return nil
}

func (r *VirtualMachineTemplateReconciler) handleFinalizer(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(vmTemplate, virtualMachineTemplateFinalizerName) {
		controllerutil.AddFinalizer(vmTemplate, virtualMachineTemplateFinalizerName)
		if err := r.Update(ctx, vmTemplate); err != nil {
			logger.Error(err, "Failed to update VirtualMachineTemplate with finalizer")
			return err
		}
	}
	return nil
}

func (r *VirtualMachineTemplateReconciler) deleteVirtualMachineTemplate(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) {
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("Deleting VirtualMachineTemplate %s", vmTemplate.Name))
	// Delete the VM
	r.Recorder.Event(vmTemplate, "Normal", "VMTemplateDeleting", fmt.Sprintf("VirtualMachine template %s is deleting", vmTemplate.Name))
	if vmTemplate.Spec.DeletionProtection {
		logger.Info("Deletion protection is enabled, skipping the deletion of VM")
		return
	} else {
		proxmox.DeleteVM(vmTemplate.Spec.Name, vmTemplate.Spec.NodeName)
	}
}

func (r *VirtualMachineTemplateReconciler) createStorageDownloadURLCR(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	// If storageDownloadURL is not found, create a new one
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(imageNameFormat, vmTemplate.Name),
			Namespace: vmTemplate.Namespace,
		},
		Spec: proxmoxv1alpha1.StorageDownloadURLSpec{
			Content:  "iso",
			Filename: vmTemplate.Spec.ImageConfig.Filename,
			Node:     vmTemplate.Spec.ImageConfig.Node,
			Storage:  vmTemplate.Spec.ImageConfig.Storage,
			URL:      vmTemplate.Spec.ImageConfig.URL,
		},
	}
	// Set VirtualMachineTemplate object as the owner and controller
	if err := controllerutil.SetControllerReference(vmTemplate, storageDownloadURL, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, storageDownloadURL); err != nil {
		logger.Error(err, "Failed to create StorageDownloadURL")
		return err
	}
	return nil
}

func (r *VirtualMachineTemplateReconciler) handleCloudInitOperations(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	templateVMName := vmTemplate.Spec.Name
	nodeName := vmTemplate.Spec.NodeName
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, client.ObjectKey{Name: fmt.Sprintf(imageNameFormat, vmTemplate.Name),
		Namespace: vmTemplate.Namespace}, storageDownloadURL)
	if err != nil {
		err = r.handleResourceNotFound(ctx, err)
		if err != nil {
			return err
		}
	}
	// Continue with the VM template operations
	// 1. Import Disk
	err = proxmox.ImportDiskToVM(templateVMName, nodeName, storageDownloadURL.Spec.Filename)
	if err != nil {
		logger.Error(err, "Failed to import disk to VM")
		return err
	}
	// 2. Add cloud Init CD-ROM drive
	err = proxmox.AddCloudInitDrive(templateVMName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to add cloud-init drive to VM")
		return err
	}
	// 3. Set cloud-init configuration
	err = proxmox.SetCloudInitConfig(templateVMName, nodeName, &vmTemplate.Spec.CloudInitConfig)
	if err != nil {
		logger.Error(err, "Failed to set cloud-init configuration")
		return err
	}
	// 4. Set boot order to boot from imported disk
	err = proxmox.SetBootOrder(templateVMName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to set boot order")
		return err
	}
	// 5. Convert VM to template
	err = proxmox.ConvertVMToTemplate(templateVMName, nodeName)
	if err != nil {
		logger.Error(err, "Failed to convert VM to template")
		return err
	}
	return nil
}

func (r *VirtualMachineTemplateReconciler) handleStorageDownloadURL(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// Make sure the the image is downloaded and ready. If not, requeue the request
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, client.ObjectKey{Name: fmt.Sprintf(imageNameFormat, vmTemplate.Name),
		Namespace: vmTemplate.Namespace}, storageDownloadURL)
	if err != nil {
		err = r.handleResourceNotFound(ctx, err)
		if err != nil {
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	if storageDownloadURL.Status.Status != "Completed" {
		logger.Info("Image is not ready, requeue the request")
		return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
	}
	// Update the condition for the VirtualMachineTemplate if it's not already available
	if !meta.IsStatusConditionPresentAndEqual(vmTemplate.Status.Conditions, typeAvailableVirtualMachineTemplate, metav1.ConditionTrue) {
		meta.SetStatusCondition(&vmTemplate.Status.Conditions, metav1.Condition{
			Type:    typeAvailableVirtualMachineTemplate,
			Status:  metav1.ConditionTrue,
			Reason:  "Available",
			Message: "VirtualMachineTemplate is available",
		})
		if err = r.Status().Update(ctx, vmTemplate); err != nil {
			logger.Error(err, "Failed to update VirtualMachineTemplate status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineTemplateReconciler) handleStatus(ctx context.Context,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !meta.IsStatusConditionPresentAndEqual(vmTemplate.Status.Conditions, typeAvailableVirtualMachineTemplate, metav1.ConditionTrue) {
		meta.SetStatusCondition(&vmTemplate.Status.Conditions, metav1.Condition{
			Type:    typeAvailableVirtualMachineTemplate,
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "VirtualMachineTemplate is ready",
		})
		if err := r.Status().Update(ctx, vmTemplate); err != nil {
			logger.Error(err, "Failed to update VirtualMachineTemplate status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{}, nil
}

func (r *VirtualMachineTemplateReconciler) handleWatcher(ctx context.Context, req ctrl.Request,
	vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) {
	r.Watchers.HandleWatcher(ctx, req, func(ctx context.Context, stopChan chan struct{}) (ctrl.Result, error) {
		return proxmox.StartWatcher(ctx, vmTemplate, stopChan, r.fetchResource, r.updateStatus,
			r.checkDelta, r.handleAutoStartFunc, r.handleReconcileFunc, r.Watchers.DeleteWatcher, r.IsResourceReady)
	})
}

func (r *VirtualMachineTemplateReconciler) fetchResource(ctx context.Context, key client.ObjectKey, obj proxmox.Resource) error {
	return r.Get(ctx, key, obj.(*proxmoxv1alpha1.VirtualMachineTemplate))
}

func (r *VirtualMachineTemplateReconciler) updateStatus(ctx context.Context, obj proxmox.Resource) error {
	return r.Status().Update(ctx, obj.(*proxmoxv1alpha1.VirtualMachineTemplate))
}

func (r *VirtualMachineTemplateReconciler) checkDelta(obj proxmox.Resource) (bool, error) {
	return proxmox.CheckVirtualMachineTemplateDelta(obj.(*proxmoxv1alpha1.VirtualMachineTemplate))
}

// TODO: Try to remove the requirement of handleAutoStartFunc
func (r *VirtualMachineTemplateReconciler) handleAutoStartFunc(_ context.Context, _ proxmox.Resource) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *VirtualMachineTemplateReconciler) handleReconcileFunc(ctx context.Context, obj proxmox.Resource) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}})
}

func (r *VirtualMachineTemplateReconciler) IsResourceReady(obj proxmox.Resource) (bool, error) {
	return proxmox.IsVirtualMachineReady(obj.(*proxmoxv1alpha1.VirtualMachineTemplate))
}

func (r *VirtualMachineTemplateReconciler) handleDelete(ctx context.Context,
	req ctrl.Request, vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if !meta.IsStatusConditionPresentAndEqual(vmTemplate.Status.Conditions, typeDeletingVirtualMachineTemplate, metav1.ConditionUnknown) {
		meta.SetStatusCondition(&vmTemplate.Status.Conditions, metav1.Condition{
			Type:    typeDeletingVirtualMachineTemplate,
			Status:  metav1.ConditionUnknown,
			Reason:  "Deleting",
			Message: "VirtualMachineTemplate is being deleted",
		})
		if err := r.Status().Update(ctx, vmTemplate); err != nil {
			logger.Error(err, "Failed to update VirtualMachineTemplate status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	} else {
		return ctrl.Result{}, nil
	}
	// TODO: Stop the watcher if resource is being deleted
	// Stop the watcher if resource is being deleted
	if stopChan, exists := r.Watchers.Watchers[req.Name]; exists {
		close(stopChan)
		delete(r.Watchers.Watchers, req.Name)
	}
	// Delete the VirtualMachineTemplate
	r.deleteVirtualMachineTemplate(ctx, vmTemplate)
	// Remove the finalizer
	logger.Info("Removing finalizer from VirtualMachineTemplate", "name", vmTemplate.Name)
	controllerutil.RemoveFinalizer(vmTemplate, virtualMachineTemplateFinalizerName)
	if err := r.Update(ctx, vmTemplate); err != nil {
		logger.Error(err, "Failed to remove VirtualMachineTemplate finalizer")
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}
