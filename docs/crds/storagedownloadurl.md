# StorageDownloadURL

`StorageDownloadURL` is a custom resource that represents a download URL for a file in a storage account. You can download "iso" or "vztmpl" files from the internet and store them in a storage for Proxmox. 


## Creating StorageDownloadURL

To create a new StorageDownloadURL in Proxmox, you need to create a `StorageDownloadURL` object.

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: StorageDownloadURL
metadata:
  name: storagedownloadurl-sample
spec:
  connectionRef:
    name: proxmox-connection-sample
  content: "iso" 
  filename: "ubuntu-20.04-server-cloudimg-amd64.img"
  node: "lowtower"
  storage: "local" 
  url: "https://cloud-images.ubuntu.com/releases/focal/release/ubuntu-20.04-server-cloudimg-amd64.img"
```
