[![Pipeline Status](https://img.shields.io/github/actions/workflow/status/alperencelik/kubemox/.github/workflows/publish.yaml?branch=main)](https://github.com/alperencelik/kubemox/actions)
[![Latest Release](https://img.shields.io/github/v/release/alperencelik/kubemox)](https://github.com/alperencelik/kubemox/releases)

# Kubemox

Kubemox is a Proxmox operator for Kubernetes. It allows you to create and manage Proxmox VMs from Kubernetes.

<div style="text-align:center;">
  <img src="docs/images/kubemox.jpg" alt="Logo" width="150" height="150">
</div>

## Why Kubemox?

Proxmox is a great open-source virtualization platform. It has a great API and CLI but managing resources inside Proxmox within a declarative way might be hard. Kubemox is a Kubernetes operator that allows you to manage Proxmox resources in a declarative way. It brings the power of Kubernetes to Proxmox and allows you to manage Proxmox resources with Kubernetes resources with the endless control loop of Kubernetes.

Kubemox helps you to manage your infrastructure components in a declarative way. You can also combine with GitOps tool to make your infrastructure immutable and reproducible. See the documentation section for more information.


## Documentation

Documentation is available at [https://alperencelik.github.io/kubemox/](https://alperencelik.github.io/kubemox/). 

<!--
## Table of Contents

- [Installation](#installation)
  - [Prerequisites](#prerequisites)
  - [Install Kubemox with Helm](#install-kubemox-with-helm)
- [Usage](#usage)
  - [VirtualMachine](#virtualmachine)
  - [Containers](#containers)
- [Create a Container](#create-a-container) 
- [Create a VirtualMachine](#create-a-virtualmachine)
- [Create a VirtualMachineSet](#create-a-virtualmachineset)
- [Create a VirtualMachineSnapshot](#create-a-virtualmachinesnapshot)
- [Create a VirtualMachineSnapshotPolicy](#create-a-virtualmachinesnapshotpolicy)

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

Currently Kubemox brings five different CRDs for only VirtualMachines in Proxmox. These are `VirtualMachine`, `VirtualMachineSet`, `ManagedVirtualMachine`, `VirtualMachineSnapshot` and `VirtualMachineSnapshotPolicy`. You can use these CRDs to create and manage VirtualMachine(s) in Proxmox. 

### VirtualMachine

`VirtualMachine` is a way to create new VirtualMachines in Proxmox via operator. You can create `VirtualMachine` resource and Kubemox will create it for you in Proxmox. `VirtualMachine` is also reconciled by the operator which means every change on `VirtualMachine` resource will be reflected to Proxmox as well. 

`VirtualMachineSet` is a way to create multiple VirtualMachines in Proxmox. The relationship between `VirtualMachineSet` and `VirtualMachine` is similar to the relationship between `Deployment` and `Pod`. `VirtualMachineSet` creates multiple `VirtualMachine` resources and Kubemox will create them for you in Proxmox. You can only use `VirtualMachineSet` with templates. Creating multiple VirtualMachines from scratch is not supported yet. 

`ManagedVirtualMachine` is a way to bring your existing VirtualMachines in Proxmox to Kubernetes. As an user you don't need to create `ManagedVirtualMachine` resource. Kubemox will create it for you after the deployment at startup of controller. `ManagedVirtualMachine` is also reconciled by the operator so if you do any change on those (delete, update, etc.) it will be reflected to Proxmox. 

`VirtualMachineSnapshot` is helping to create snapshots for `VirtualMachine` object. This object mostly considered for the milestone snapshots. This will create only one snapshot for the `VirtualMachine` object. Also deleting the `VirtualMachineSnapshot` object won't be deleting the snapshot from Proxmox since the current proxmox client the project uses doesn't have an implementation for deleting snapshots.

`VirtualMachineSnapshotPolicy` is helping to create snapshots for `VirtualMachine` object periodically. This object mostly considered for the scheduled snapshots. The schedule and the selectors that you specify matches with the `VirtualMachine` objects and according to the schedule it will create snapshots for those `VirtualMachine` objects. `VirtualMachineSnapshotPolicy` will be spawning `VirtualMachineSnapshot` objects for each `VirtualMachine` object that matches with the selectors. Also deleting the `VirtualMachineSnapshotPolicy` object also won't be deleting the snapshots from Proxmox but it will stop creating new `VirtualMachineSnapshot` objects for the `VirtualMachine` objects that matches with the selectors.

### Containers

`Container` is a way to create new Linux containers (LXC) in Proxmox via operator. You can create `Container` resource and Kubemox will create it for you in Proxmox. `Container` is also reconciled by the operator which means every change on `Container` resource will be reflected to Proxmox as well. `Container` object should be generated by other container template objects. This is a requirement that comes from Proxmox itself.

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


### Create a VirtualMachineSnapshot

To create a VirtualMachineSnapshot you can use the following `VirtualMachineSnapshot` resource:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSnapshot
metadata:
  name: test-vm-snapshot
spec:
  vmName: test-vm
  # snapshotName is optional ( if you don't specify it will create a snapshot with the current date )
  snapshotName: test-vm-snapshot
```

### Create a VirtualMachineSnapshotPolicy

To create a VirtualMachineSnapshotPolicy you can use the following `VirtualMachineSnapshotPolicy` resource:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachineSnapshotPolicy
metadata:
  name: test-vm-snapshot-policy
spec:
  schedule: "*/30 * * * *" # Create a snapshot every 30 minutes
  namespaceSelector: ["my-namespace"]
    matchLabels:
      my-label: my-virtualmachine
```

### Create a Container

To create a Container you can use the following `Container` resource:

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: Container
metadata:
  name: container-new
spec:
  name: container-new
  nodeName: lowtower
  template:
    # This template should be generated by other container template object
    name: test-container 
    cores: 2
    memory: 4096 # As MB
    disk: 
      - storage: nvme 
        size: 50 # As GB
        type: scsi
    network:
      - model: virtio
        bridge: vmbr0
```

To learn more about `VirtualMachine`, `VirtualMachineSet`, `Container` and `VirtualMachineSnapshot` resources you can check `charts/kubemox/samples/`
-->

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
