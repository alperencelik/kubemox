# Virtual MachineSet

`VirtualMachineSet` is a way to create multiple VirtualMachines in Proxmox. The relationship between `VirtualMachineSet` and `VirtualMachine` is similar to the relationship between `Deployment` and `Pod`. `VirtualMachineSet` creates multiple `VirtualMachine` resources and Kubemox will create them for you in Proxmox. You can only use `VirtualMachineSet` with templates. Creating multiple VirtualMachines from scratch is not supported yet.

## Creating VirtualMachineSet

To create a new virtual machine in Proxmox, you need to create a `VirtualMachineSet` object. VirtualMachineSet allows you to create multiple VirtualMachines from a template. 

```yaml
# This manifest is used to create a VirtualMachineSet from an existing template.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSet
metadata:
  labels:
    app.kubernetes.io/name: virtualmachineset
    app.kubernetes.io/instance: virtualmachineset-sample
    app.kubernetes.io/part-of: kubemox
    app.kubernetes.io/managed-by: kustomize
    app.kubernetes.io/created-by: kubemox
  name: virtualmachineset-test
spec:
  connectionRef:
    name: proxmox-connection-sample
  # Number of VMs to be created
  replicas: 3
  nodeName: lowtower
  # Deletion protection is whether to delete VM from Proxmox or not
  deleteProtection: false
  # VM should be started any time found in stopped state
  enableAutostart: true
  template:
    # Name of the template to be cloned
    name: centos-template-new
    # CPU cores to be allocated to the VM
    cores: 2
    # CPU sockets to be allocated to the VM
    socket: 1
    # Memory to be allocated to the VM
    memory: 4096 # As MB
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
