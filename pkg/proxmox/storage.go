package proxmox

import (
	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/luthermonson/go-proxmox"
)

func StorageDownloadURL(node string, storageDownloadURLSpec *proxmoxv1alpha1.StorageDownloadURLSpec) (string, error) {
	// Get node
	Node, err := Client.Node(ctx, node)
	if err != nil {
		panic(err)
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
		panic(err)
	}
	return response, err
}

func GetStorageContent(node string, storageName string) (*proxmox.StorageContent, error) {
	// Get node
	Node, err := Client.Node(ctx, node)
	if err != nil {
		panic(err)
	}
	storage, _ := Node.Storage(ctx, storageName)
	// Get storage content
	content, err := storage.GetContent(ctx)
	if err != nil {
		panic(err)
	}
	return content, err
}
