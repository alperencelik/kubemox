# Getting Started

!!! tip
    This guide assumes you have a Proxmox VE cluster up and running. If you don't have one, you can follow the [official guide](https://pve.proxmox.com/wiki/Installation) to install Proxmox VE.

## Requirements

* Kubernetes cluster with version 1.16+
* Helm 3+
* Proxmox VE with version 6.3+

## Installation

1. Install Kubemox with Helm

    ```bash
    helm repo add alperencelik https://alperencelik.github.io/kubemox
    helm install kubemox alperencelik/kubemox --set \
        proxmox.endpoint="https://<PROXMOX_HOSTNAME" \
        proxmox.insecureSkipTLSVerify=false \
        proxmox.username="<PROXMOX_USERNAME>" \
        proxmox.password="<PROXMOX_PASSWORD>"
    ```

!!! tip
    You can use also tokenID and secret instead of username and password.
        proxmox.tokenID="<PROXMOX_TOKEN_ID>" \
        proxmox.secret="<PROXMOX_SECRET>"

!!! warning
    If you are using self-signed certificates, you should set `proxmox.insecureSkipTLSVerify` to `true`.

!!! warning
    Make sure that the user you specified has the necessary permissions to create Proxmox VE resources. 


2. Clone from the source

    1. You can also clone the repository and run the operator locally with Make. 

    ```
    git clone https://github.com/alperencelik/kubemox.git
    make install ## Install CRDs
    export PROXMOX_ENDPOINT="https://<PROXMOX_HOSTNAME>"
    export PROXMOX_USERNAME="<PROXMOX_USERNAME>"
    export PROXMOX_PASSWORD="<PROXMOX_PASSWORD>"
    export PROXMOX_INSECURE_SKIP_TLS_VERIFY=false
    make run ## Run the operator locally with the current kubeconfig
    ```

    2. You can also run the operator locally with the provided Tiltfile. 

    You can download the Tilt binary from [here](https://docs.tilt.dev/install.html). 
    Update the Tiltfile with your Proxmox VE credentials.

    ```
    tilt up
    ```
 