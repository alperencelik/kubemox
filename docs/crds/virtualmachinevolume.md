# VirtualMachineVolume

`VirtualMachineVolume` is a disk that outlives the virtual machine it is attached to. You create it once, with a size and optionally a source to copy from, and attach it to a `VirtualMachine` later; deleting that `VirtualMachine` gives the disk back instead of destroying it.

## How it is kept

Proxmox has no standalone volume. Every volume belongs to a VMID, and `qm destroy` takes the volumes that are still attached with it. So the operator keeps each namespace's volumes on a **holder**: a VM named `kubemox-disks-<namespace>-<node>` that has no network, the smallest memory Proxmox accepts, `protection=1`, and never starts. A volume sits on the holder as a regular drive slot (`scsi0`, `scsi1`, …) while nothing uses it, and is reassigned to a `VirtualMachine` with Proxmox's `move_disk` when one wants it.

Reassignment only works between VMs on the same node, so `spec.nodeName` is where the volume lives and where a `VirtualMachine` has to run to use it. A node has one holder per namespace; a holder has room for about 54 volumes, after which the operator opens a second one.

The volume id in `status.volume` is informational: Proxmox renames a volume after its owner every time it moves (`vm-9998-disk-0` becomes `vm-9999-disk-1` and back), and the operator re-reads it after each move.

## Creating a VirtualMachineVolume

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineVolume
metadata:
  name: web-system
  namespace: team-a
spec:
  connectionRef:
    name: proxmox-connection-sample
  nodeName: lowtower
  storage: local-zfs
  size: 20
  source:
    volume: local:import/debian-12-genericcloud-amd64.qcow2
```

| Field | Meaning |
|---|---|
| `nodeName` | Proxmox node the volume is created on. Immutable. |
| `storage` | Proxmox storage the volume is allocated on, as in `pvesm status`. Immutable. |
| `size` | Size in GiB. It can be raised later, which grows the volume in place; it cannot be lowered. |
| `source` | Where the data comes from. Leave it out for a blank disk. Immutable. |
| `source.volume` | A Proxmox volume id to copy with `import-from`: a file on a storage with the `import` content type (`local:import/debian.qcow2`), or the disk of a template (`local-zfs:base-9001-disk-0`). The copy is grown to `size` when the source is smaller. |
| `source.disk` | Another `VirtualMachineVolume` to copy: `name`, and `namespace` when it is not this one. It has to be Ready, on the same node, and not attached while the copy is made. |
| `cloneable` | Let volumes in other namespaces copy this one. Default `false`: a volume is only ever copied inside its own namespace. |
| `deletionProtection` | Keep the volume on the holder when this object is deleted. Default `false`: deleting the object frees the volume. |

Copying from a file needs a storage with the `import` content type (`pvesm set local --content backup,iso,vztmpl,import` on Proxmox VE 8.4 or later); copying from a template disk or another volume does not.

## Copying across namespaces

A volume is copied inside its own namespace freely. A copy from another namespace is made only when the source carries `cloneable: true`; otherwise the new volume waits with `Ready=False`, reason `SourceNotCloneable`, and goes on as soon as the owner sets the flag. This is how a platform keeps golden images in one namespace and lets every tenant start from them:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineVolume
metadata:
  name: debian-12
  namespace: images
spec:
  connectionRef:
    name: proxmox-connection-sample
  nodeName: lowtower
  storage: local-zfs
  size: 10
  cloneable: true
  source:
    volume: local:import/debian-12-genericcloud-amd64.qcow2
---
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineVolume
metadata:
  name: web-system
  namespace: team-a
spec:
  connectionRef:
    name: proxmox-connection-sample
  nodeName: lowtower
  storage: local-zfs
  size: 20
  source:
    disk:
      name: debian-12
      namespace: images
```

The copy is a full `import-from` of the source's current volume, so the source may be grown, re-attached or deleted afterwards without touching the copies. Both volumes have to be on the same node: a Proxmox volume is reachable from the node that owns it, and `import-from` runs there.

## Status

```shell
kubectl get vmvol -n team-a
NAME         NODE       STORAGE     SIZE   PHASE   VOLUME                        ATTACHED TO   AGE
web-system   lowtower   local-zfs   20     Ready   local-zfs:vm-9998-disk-0                    2m
```

| Phase | Meaning |
|---|---|
| `Pending` | The volume is being created or copied, or waits for its source. The `Ready` condition says which. |
| `Ready` | The volume sits on the holder, free to be attached. |
| `Attached` | A `VirtualMachine` has it; `status.attachedTo` names it and `status.slot` is the drive on that VM. |
| `Failed` | Proxmox refused. The `Ready` condition carries Proxmox's message; a change to the spec starts over. |

A `VirtualMachineVolume` that is attached cannot be deleted: the finalizer waits with `Ready=False, reason InUse` until the `VirtualMachine` lets go of it.

## Permissions

The connection's token needs `VM.Allocate`, `VM.Config.Disk` and `VM.Config.Options` on the holder and the VMs it serves, and `Datastore.AllocateSpace` on `spec.storage`. Copying from a file under `import` additionally needs `Datastore.Audit` on that storage.
