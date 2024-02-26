package proxmox

import (
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func StorageDownloadURL(node string, storageDownloadURLSpec *proxmoxv1alpha1.StorageDownloadURLSpec) (string, error) {
	// Get node
	Node, err := Client.Node(ctx, node)
	if err != nil {
		log.Log.Error(err, "unable to get node")
	}
	storageDownloadURLOptions := proxmox.StorageDownloadURLOptions{
		Content:  storageDownloadURLSpec.Content,
		Filename: storageDownloadURLSpec.Filename,
		Node:     storageDownloadURLSpec.Node,
		Storage:  storageDownloadURLSpec.Storage,
		URL:      storageDownloadURLSpec.URL,
		// Optional parameters
		Checksum:          storageDownloadURLSpec.Checksum,
		ChecksumAlgorithm: storageDownloadURLSpec.ChecksumAlgorithm,
		Compression:       storageDownloadURLSpec.Compression,
	}
	// Post request to get download URL
	response, err := Node.StorageDownloadURL(ctx, &storageDownloadURLOptions)
	if err != nil {
		log.Log.Error(err, "unable to start download operation")
	}
	return response, err
}

func GetStorageContent(node, storageName string) ([]*proxmox.StorageContent, error) {
	// Get node
	Node, err := Client.Node(ctx, node)
	if err != nil {
		log.Log.Error(err, "unable to get node")
	}
	storage, _ := Node.Storage(ctx, storageName)
	// Get storage content
	content, err := storage.GetContent(ctx)
	if err != nil {
		log.Log.Error(err, "unable to get storage content")
	}
	return content, err
}

func HasFile(storageContent []*proxmox.StorageContent, storageDownloadSpec *proxmoxv1alpha1.StorageDownloadURLSpec) bool {
	targetFile := fmt.Sprintf("%s:%s/%s", storageDownloadSpec.Storage, storageDownloadSpec.Content, storageDownloadSpec.Filename)
	for _, item := range storageContent {
		if item.Volid == targetFile {
			return true
		}
	}
	return false
}

func DeleteStorageContent(storageName string, spec *proxmoxv1alpha1.StorageDownloadURLSpec) error {
	// Get node
	node := spec.Node
	Node, err := Client.Node(ctx, node)
	if err != nil {
		log.Log.Error(err, "unable to get node")
	}
	storage, _ := Node.Storage(ctx, storageName)
	// Delete storage content
	// local:iso/talos-amd64.iso
	objectName := fmt.Sprintf("%s:%s/%s", storageName, spec.Content, spec.Filename)
	task, err := storage.DeleteContent(ctx, objectName)
	if err != nil {
		log.Log.Error(err, "unable to delete storage content")
	}
	// Wait for task to complete
	_, taskCompleted, err := task.WaitForCompleteStatus(ctx, 5, 10)
	if taskCompleted {
		log.Log.Info(fmt.Sprintf("%s file has been deleted from %s successfully", spec.Filename, storageName))
	}
	if err != nil {
		log.Log.Error(err, "unable to delete storage content")
	}
	return err
}
