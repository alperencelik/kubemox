# VirtualMachine

`VirtualMachine` is a way to create new VirtualMachines in Proxmox via operator. You can create `VirtualMachine` resource and Kubemox will create it for you in Proxmox. `VirtualMachine` is also reconciled by the operator which means every change on `VirtualMachine` resource will be reflected to Proxmox as well. 

## Creating VirtualMachine

To create a new virtual machine in Proxmox, you need to create a `VirtualMachine` object. You can create two types of `VirtualMachine` objects. You can create a new virtual machine from scratch or you can create one from a template.

- Creating a new virtual machine from a template

```yaml
# This manifest is used to create a Virtual Machine from an existing template.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone
spec:
  name: virtualmachine-sample-clone
  # Name of the node where the VM will be created
  nodeName: lowtower
  template:
    # Name of the template to be cloned
    name: centos-template-new
    # CPU cores to be allocated to the VM
    cores: 2
    # CPU sockets to be allocated to the VM
    socket: 1
    # Memory to be allocated to the VM
    memory: 4096 # As MB
    # Deletion protection is whether to delete VM from Proxmox or not
    deleteProtection: false
    # VM should be started any time found in stopped state
    enableAutostart: true
    # Disk used by the VM
    disk: 
      - storage: nvme 
        size: 50 # As GB
        device: scsi0
    # Network interfaces used by the VM
    network:
      - model: virtio
        bridge: vmbr0
EOF
```

- Creating a new virtual machine from scratch

```yaml
# This manifest is used to create a VirtualMachine from scratch.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  labels:
  name: virtualmachine-sample-scratch
spec:
  name: virtualmachine-sample-scratch
  # Name of the node where the VM will be created
  nodeName: lowtower
  # Spec for the VM
  vmSpec: 
    cores: 2
    memory: 4096
    disk: 
      name: scsi0 
      value: "local-lvm:40"
    network:
      name: net0
      value: virtio,bridge=vmbr0
    osImage:
      name: ide2 
      value: local:iso/ubuntu-23.04-live-server-amd64.iso
EOF
```
