package proxmox

import (
	"context"
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/luthermonson/go-proxmox"
)

func (pc *ProxmoxClient) StorageDownloadURL(ctx context.Context, node string,
	storageDownloadURLSpec *proxmoxv1alpha1.StorageDownloadURLSpec) (string, error) {
	// Get node
	Node, err := pc.getNode(ctx, node)
	if err != nil {
		return "", fmt.Errorf("unable to get node %s: %w", node, err)
	}
	storageDownloadURLOptions := proxmox.StorageDownloadURLOptions{
		Content:            storageDownloadURLSpec.Content,
		Filename:           storageDownloadURLSpec.Filename,
		Node:               storageDownloadURLSpec.Node,
		Storage:            storageDownloadURLSpec.Storage,
		URL:                storageDownloadURLSpec.URL,
		Checksum:           storageDownloadURLSpec.Checksum,
		ChecksumAlgorithm:  storageDownloadURLSpec.ChecksumAlgorithm,
		Compression:        storageDownloadURLSpec.Compression,
		VerifyCertificates: proxmox.IntOrBool(storageDownloadURLSpec.VerifyCertificate),
	}
	// Post request to get download URL
	response, err := Node.StorageDownloadURL(ctx, &storageDownloadURLOptions)
	if err != nil {
		return "", fmt.Errorf("unable to start download operation: %w", err)
	}
	return response, nil
}

func (pc *ProxmoxClient) GetStorageContent(ctx context.Context, node,
	storageName string) ([]*proxmox.StorageContent, error) {
	// Get node
	Node, err := pc.getNode(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("unable to get node %s: %w", node, err)
	}
	storage, err := Node.Storage(ctx, storageName)
	if err != nil {
		return nil, fmt.Errorf("unable to get storage %s: %w", storageName, err)
	}
	// Get storage content
	content, err := storage.GetContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get storage content: %w", err)
	}
	return content, nil
}

func HasFile(storageContent []*proxmox.StorageContent,
	storageDownloadSpec *proxmoxv1alpha1.StorageDownloadURLSpec) bool {
	targetFile := fmt.Sprintf("%s:%s/%s", storageDownloadSpec.Storage,
		storageDownloadSpec.Content, storageDownloadSpec.Filename)
	for _, item := range storageContent {
		if item.Volid == targetFile {
			return true
		}
	}
	return false
}

func (pc *ProxmoxClient) DeleteStorageContent(ctx context.Context, storageName string,
	spec *proxmoxv1alpha1.StorageDownloadURLSpec) error {
	// Get node
	node := spec.Node
	Node, err := pc.getNode(ctx, node)
	if err != nil {
		return fmt.Errorf("unable to get node %s: %w", node, err)
	}
	storage, err := Node.Storage(ctx, storageName)
	if err != nil {
		return fmt.Errorf("unable to get storage %s: %w", storageName, err)
	}
	// Delete storage content
	objectName := fmt.Sprintf("%s:%s/%s", storageName, spec.Content, spec.Filename)
	task, err := storage.DeleteContent(ctx, objectName)
	if err != nil {
		return fmt.Errorf("unable to delete storage content %s: %w", objectName, err)
	}
	// Wait for task to complete
	_, _, err = task.WaitForCompleteStatus(ctx, 5, 10)
	if err != nil {
		return fmt.Errorf("unable to wait for delete task: %w", err)
	}
	return nil
}

func (pc *ProxmoxClient) GetStorage(ctx context.Context, storageName string) (*proxmox.ClusterStorage, error) {
	storage, err := pc.Client.ClusterStorage(ctx, storageName)
	if err != nil {
		return nil, fmt.Errorf("unable to get storage %s: %w", storageName, err)
	}
	return storage, nil
}
