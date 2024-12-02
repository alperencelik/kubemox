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

## Advanced Configuration

You can also configure advanced settings for the virtual machine object. You can set the following fields in the `VirtualMachine` object.

- Create a new virtual machine from a template

```yaml
# This manifest is used to create a Virtual Machine from an existing template with advanced configuration.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone-new
  namespace: default
spec:
  name: virtualmachine-sample-clone-new
  nodeName: lowtower
  deletionProtection: false
  enableAutoStart: true
  template:
    socket: 1
    cores: 2
    disk:
    - device: scsi0
      size: 60
      storage: local-lvm
    - device: scsi1
      size: 20
      storage: local-lvm
    memory: 4096
    name: fedora-template
    network:
    - bridge: vmbr0
      model: virtio
    pciDevices:
      - device: "c0-p0-o0-if0"
        type: "mapped"
      - device: "0000:03:00.2"
        type: "raw"
        primaryGPU: true
        pcie: true
  additionalConfig:
    balloon: "0"
    cpu: host
    vga: type=virtio,memory=16
EOF
```

- Create a new virtual machine from scratch

```yaml
# This manifest is used to create a VirtualMachine from scratch with advanced configuration.
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
    socket: 1
    cores: 2
    memory: 4096
    disk: 
      - storage: local-lvm
        size: 60
        device: scsi0
    network:
      - model: virtio
        bridge: vmbr0
    pciDevices:
      - device: "c0-p0-o0-if1"
        type: "mapped"
    osImage:
      name: ide0
      value: local:iso/ubuntu-cloud-21.04-amd64.iso
  cloudInitConfig:
    user: "ubuntu"
    passwordFrom: 
        name: cloudinit-secret
        key: password
    upgradePackages: true
    custom:
      userData: "ubuntu-cloud-config-workers.yaml" # That should be inside snippets folder in Proxmox node
EOF
```
