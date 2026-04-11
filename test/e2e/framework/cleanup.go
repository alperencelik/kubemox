package framework

import (
	"context"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Cleanup removes all e2e test resources. Best-effort: errors are logged but not returned.
func (f *Framework) Cleanup(ctx context.Context) {
	logger := log.FromContext(ctx).WithName("e2e-cleanup")

	// Delete all VirtualMachines
	vmList := &proxmoxv1alpha1.VirtualMachineList{}
	if err := f.KubeClient.List(ctx, vmList); err == nil {
		for i := range vmList.Items {
			if err := f.KubeClient.Delete(ctx, &vmList.Items[i]); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete VirtualMachine", "name", vmList.Items[i].Name)
			}
		}
	}

	// Delete all Containers
	containerList := &proxmoxv1alpha1.ContainerList{}
	if err := f.KubeClient.List(ctx, containerList); err == nil {
		for i := range containerList.Items {
			if err := f.KubeClient.Delete(ctx, &containerList.Items[i]); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete Container", "name", containerList.Items[i].Name)
			}
		}
	}

	// Delete all VirtualMachineSnapshots
	snapList := &proxmoxv1alpha1.VirtualMachineSnapshotList{}
	if err := f.KubeClient.List(ctx, snapList); err == nil {
		for i := range snapList.Items {
			if err := f.KubeClient.Delete(ctx, &snapList.Items[i]); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete VirtualMachineSnapshot", "name", snapList.Items[i].Name)
			}
		}
	}

	// Delete all VirtualMachineTemplates
	vmtList := &proxmoxv1alpha1.VirtualMachineTemplateList{}
	if err := f.KubeClient.List(ctx, vmtList); err == nil {
		for i := range vmtList.Items {
			if err := f.KubeClient.Delete(ctx, &vmtList.Items[i]); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete VirtualMachineTemplate", "name", vmtList.Items[i].Name)
			}
		}
	}

	// Delete all StorageDownloadURLs
	sduList := &proxmoxv1alpha1.StorageDownloadURLList{}
	if err := f.KubeClient.List(ctx, sduList); err == nil {
		for i := range sduList.Items {
			if err := f.KubeClient.Delete(ctx, &sduList.Items[i]); client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to delete StorageDownloadURL", "name", sduList.Items[i].Name)
			}
		}
	}

	// Delete the e2e ProxmoxConnection
	if err := f.DeleteProxmoxConnection(ctx); err != nil {
		logger.Error(err, "failed to delete ProxmoxConnection")
	}
}
