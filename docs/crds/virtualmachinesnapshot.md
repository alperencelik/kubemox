# VirtualMachineSnapshot

`VirtualMachineSnapshot` is helping to create snapshots for `VirtualMachine` object. This object mostly considered for the milestone snapshots. This will create only one snapshot for the `VirtualMachine` object. Also deleting the `VirtualMachineSnapshot` object won't be deleting the snapshot from Proxmox since the current proxmox client the project uses doesn't have an implementation for deleting snapshots.

## Creating VirtualMachineSnapshot

To create a new snapshot for a virtual machine in Proxmox, you need to create a `VirtualMachineSnapshot` object. 

```yaml
# This manifest is used to create a snapshot for a VirtualMachine.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSnapshot
metadata:
  labels:
    app.kubernetes.io/name: virtualmachinesnapshot
    app.kubernetes.io/instance: virtualmachinesnapshot-sample
    app.kubernetes.io/part-of: kubemox
    app.kubernetes.io/managed-by: kustomize
    app.kubernetes.io/created-by: kubemox
  name: virtualmachinesnapshot-sample
spec:
  virtualMachineName: "virtualmachine-sample-clone"
  snapshotName: "test-snapshot"
EOF
```
