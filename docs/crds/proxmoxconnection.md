# ProxmoxConnection

`ProxmoxConnection` is a resource that provides a connection to a Proxmox server. It allows the operator to interact with the Proxmox API and perform operations on Proxmox resources. The `ProxmoxConnection` resource is used to store the connection details, such as the Proxmox endpoint, username, and password.

## Creating ProxmoxConnection

To create a new `ProxmoxConnection` resource, you need to provide the connection details in the manifest. Here is an example of how to create a `ProxmoxConnection` resource:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: ProxmoxConnectioon
name: proxmox-connection-sample
spec:
  endpoint: "PROXMOX_ENDPOINT"
  username: "PROXMOX_USERNAME"
  password: "PROXMOX_PASSWORD"
  insecureSkipVerify: true
EOF
```

## Referencing your ProxmoxConnection

`ProxmoxConnection` resource itself does not create any Proxmox resources. It is used to create other Proxmox resources such as `VirtualMachine`, `Container`, and `Storage`. You can reference the `ProxmoxConnection` resource in the spec of these resources. It's a metadata object to be referenced in other resources. For example, you can reference the `ProxmoxConnection` resource in the `VirtualMachine` resource as follows:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone-new
  namespace: default
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-clone-new
  nodeName: lowtower
  template:
    socket: 1
    cores: 2
    disk:
    - device: scsi0
      size: 60
      storage: local-lvm
    memory: 4096
```
  

