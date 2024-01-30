# VirtualMachineSnapshotPolicy

`VirtualMachineSnapshotPolicy` is helping to create snapshots for `VirtualMachine` object periodically. This object mostly considered for the scheduled snapshots. The schedule and the selectors that you specify matches with the `VirtualMachine` objects and according to the schedule it will create snapshots for those `VirtualMachine` objects. `VirtualMachineSnapshotPolicy` will be spawning `VirtualMachineSnapshot` objects for each `VirtualMachine` object that matches with the selectors. Also deleting the `VirtualMachineSnapshotPolicy` object also won't be deleting the snapshots from Proxmox but it will stop creating new `VirtualMachineSnapshot` objects for the `VirtualMachine` objects that matches with the selectors.

## Creating VirtualMachineSnapshotPolicy

To create a new snapshot policy for virtual machines in Proxmox, you need to create a `VirtualMachineSnapshotPolicy` object. 

```yaml
# This manifest is used to create a snapshot policy for VirtualMachines.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSnapshotPolicy
metadata:
  labels:
    app.kubernetes.io/name: virtualmachinesnapshotpolicy
    app.kubernetes.io/instance: virtualmachinesnapshotpolicy-sample
    app.kubernetes.io/part-of: kubemox
    app.kubernetes.io/managed-by: kustomize
    app.kubernetes.io/created-by: kubemox
  name: virtualmachinesnapshotpolicy-sample
spec:
  snapshotSchedule: "*/30 * * * *" # Every 30 minutes
  namespaceSelector:
    namespaces: ["default", "my-namespace"]
  labelSelector:
    matchLabels:
      app.kubernetes.io/name: virtualmachine-sample-clone
EOF
```