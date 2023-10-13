# Kubemox

Kubemox is a Proxmox operator for Kubernetes. It allows you to create and manage Proxmox VMs from Kubernetes.

<div style="text-align:center;">
  <img src="docs/images/kubemox.jpg" alt="Logo" width="150" height="150">
</div>


## Installation

### Prerequisites

- Kubernetes cluster with version 1.16+
- Helm 3+
- Proxmox Cluster with version 6.3+

### Install Kubemox with Helm

To install Kubemox you can use the following command:

```bash
git clone https://github.com/alperencelik/kubemox.git
cd kubemox/charts/kubemox
# Edit values.yaml file (Proxmox credentials, etc.)
vim values.yaml
helm install kubemox ./ -f values.yaml -n $NAMESPACE
```

## Usage

Currently Kubemox brings three different CRDs for only VirtualMachines in Proxmox. These are `VirtualMachine`, `VirtualMachineSet`, `ManagedVirtualMachine`. You can use these CRDs to create and manage VirtualMachine(s) in Proxmox. 

`VirtualMachine` is a way to create new VirtualMachines in Proxmox via operator. You can create `VirtualMachine` resource and Kubemox will create it for you in Proxmox. `VirtualMachine` is also reconciled by the operator which means every change on `VirtualMachine` resource will be reflected to Proxmox as well. 

`VirtualMachineSet` is a way to create multiple VirtualMachines in Proxmox. The relationship between `VirtualMachineSet` and `VirtualMachine` is similar to the relationship between `Deployment` and `Pod`. `VirtualMachineSet` creates multiple `VirtualMachine` resources and Kubemox will create them for you in Proxmox. You can only use `VirtualMachineSet` with templates. Creating multiple VirtualMachines from scratch is not supported yet. 

`ManagedVirtualMachine` is a way to bring your existing VirtualMachines in Proxmox to Kubernetes. As an user you don't need to create `ManagedVirtualMachine` resource. Kubemox will create it for you after the deployment at startup of controller. `ManagedVirtualMachine` is also reconciled by the operator so if you do any change on those (delete, update, etc.) it will be reflected to Proxmox. 

### Create a VirtualMachine

To create a VirtualMachine you can use the following `VirtualMachine` resource:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1 
kind: VirtualMachine
metadata:
  name: test-vm
spec:
  name: test-vm
  nodeName: proxmox-node
  template:
    name: ubuntu-20.04-cloudinit-template
    cores: 4
    sockets: 1
    memory: 4096
    disk:
      - size: 50G
        storage: local-lvm
        type: scsi
    network:
      - model: virtio
        bridge: vmbr0
```
### Create a VirtualMachineSet

To create a VirtualMachineSet you can use the following `VirtualMachineSet` resource:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSet
metadata:
  name: test-vmset
spec:
  replicas: 3
  nodeName: lowtower
  template:
    name: ubuntu-20.04-cloudinit-template
    cores: 4
    sockets: 1
    memory: 4096
    disk:
      - size: 50G
        storage: local-lvm
        type: scsi
    network:
      - model: virtio
        bridge: vmbr0
```

To learn more about `VirtualMachine` and `VirtualMachineSet` resources you can check `charts/kubemox/samples/`


## Developing 

You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster. The project is using [Kubebuilder](book.kubebuilder.io) to generate the controller and CRDs. For Proxmox interaction the project is using [go-proxmox](https://github.com/luthermonson/go-proxmox) project. The controllers are located under `internal/controllers/proxmox` directory and the external packages `proxmox` and `kubernetes` are located under `pkg` directory.

- To create a new controller you can use the following command:

```bash
kubebuilder create api --group proxmox --version v1alpha1 --kind NewKind 
```

- Define the spec and status of your new kind in `api/proxmox/v1alpha1/newkind_types.go` file.

- Define the controller logic in `internal/controllers/proxmox/newkind_controller.go` file.


## Roadmap

- [ ] Add more CRDs for Proxmox resources (LXC(Containers), Storage, Networking etc.)
- [ ] Add more options for Proxmox client (TLS and different authentication methods)
- [ ] Add more features to the operator (HA, configuration, etc.)
- [ ] Add metrics for the operator
- [ ] Add more tests
- [ ] Add more documentation
- [ ] Add more examples


## Contributing

Thank you for considering contributing to this project! To get started, please follow these guidelines:

- If you find a bug or have a feature request, please [open an issue](https://github.com/alperencelik/kubemox/issues).
- If you'd like to contribute code, please fork the repository and create a pull request.
- Please follow our [developing.md](developing.md) in all your interactions with the project. 
- Before submitting a pull request, make sure to run the tests and ensure your code adheres to our coding standards.
