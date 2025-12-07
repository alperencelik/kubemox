# Getting Started

!!! tip
    This guide assumes you have a Proxmox VE cluster up and running. If you don't have one, you can follow the [official guide](https://pve.proxmox.com/wiki/Installation) to install Proxmox VE.

## Requirements

* Kubernetes cluster with version 1.16+
* Helm 3+
* Proxmox VE with version 6.3+

## Installation

### Install Kubemox with Helm

```bash
helm repo add alperencelik https://alperencelik.github.io/helm-charts/
helm repo update alperencelik
helm install kubemox alperencelik/kubemox
```

### Clone from the source

* You can also clone the repository and run the operator locally with Make.

```bash
git clone https://github.com/alperencelik/kubemox.git
make install ## Install CRDs
make run ## Run the operator locally with the current kubeconfig
```

* You can download the Tilt binary from [here](https://docs.tilt.dev/install.html). Update the Tiltfile with your Proxmox VE credentials.

```bash
tilt up
```

## Creating your first Proxmox resource


You can create different Proxmox resources using the Kubemox CRDs. For more information, you can check the [Custom Resources](crds/proxmoxconnection.md). Example resource manifests can be found in the [examples](https://github.com/alperencelik/kubemox/tree/main/examples) directory.