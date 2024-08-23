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

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// VirtualMachineTemplateReconciler reconciles a VirtualMachineTemplate object
type VirtualMachineTemplateReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Watchers *proxmox.ExternalWatchers
}

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

	// The logic should have two steps:
	// 1. Check if the image is exist in the Proxmox node
	// 2. If not, download the image and wait for the download to complete
	// 3. Create the VM template with the given configuration

	// Create a storageDownloadURL for the image
	// Once it's ready we can start creating the VM template

	// Handle the image operations and make sure the image is available
	err := r.handleImageOperations(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}

	// Create the VM template
	err = r.handleVMTemplateCreation(ctx, vmTemplate)
	if err != nil {
		return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineTemplate{}).
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

func (r *VirtualMachineTemplateReconciler) handleImageOperations(ctx context.Context, vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	// Create a StorageDownloadURL object
	storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
	err := r.Get(ctx, client.ObjectKey{Name: fmt.Sprintf(imageNameFormat, vmTemplate.Name), Namespace: vmTemplate.Namespace}, storageDownloadURL)
	if err != nil {
		if errors.IsNotFound(err) {
			// If storageDownloadURL is not found, create a new one
			storageDownloadURL = &proxmoxv1alpha1.StorageDownloadURL{
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
			if err := r.Create(ctx, storageDownloadURL); err != nil {
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

func (r *VirtualMachineTemplateReconciler) handleVMTemplateCreation(ctx context.Context, vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate) error {
	logger := log.FromContext(ctx)
	// Check if the VM is already exists and
	templateVMName := vmTemplate.Spec.Name
	nodeName := vmTemplate.Spec.NodeName

	// Check if the VM template is already exists and it's type is template
	if proxmox.CheckVM(templateVMName, nodeName) && proxmox.IsVMTemplate(templateVMName, nodeName) {
		logger.Info(fmt.Sprintf("VM template %s already exists", templateVMName))
		return nil
	} else {
		// Even if the VM is exists, it may not be a template but we will create a new one no matter what
		logger.Info(fmt.Sprintf("VM template %s does not exists, creating a new one", templateVMName))
		// Create the VM first
		task, err := proxmox.CreateVMTemplate(vmTemplate)
		// Wait for the task to complete
		_, taskCompleted, err := task.WaitForCompleteStatus(ctx, 3, 7)
		if !taskCompleted {
			logger.Error(err, "Failed to create VM for template")
			return err
		}
		// Make sure the the image is downloaded and ready
		// If not, requeue the request
		storageDownloadURL := &proxmoxv1alpha1.StorageDownloadURL{}
		err = r.Get(ctx, client.ObjectKey{Name: fmt.Sprintf(imageNameFormat, vmTemplate.Name), Namespace: vmTemplate.Namespace}, storageDownloadURL)
		if err != nil {
			return r.handleResourceNotFound(ctx, err)
		}
		if storageDownloadURL.Status.Status != "Completed" {
			logger.Info("Image is not ready, requeue the request")
			return nil
		} else {
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
		}

		return nil

	}

}
