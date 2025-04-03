# Talos on Proxmox

This example demonstrates how to deploy a Talos cluster on Proxmox using the `Kubemox` operator. Kubemox is a Kubernetes operator that manages resources on Proxmox. This way you don't need to any Proxmox operations manually; Kubemox takes care of everything for you!

## Prerequisites

- A Proxmox host/cluster.
- A Kubernetes cluster with the `Kubemox` operator installed. [Installation instructions](https://alperencelik.github.io/kubemox/getting_started/)
- Installed tools: `kubectl` and `talosctl`.

## Deployment Steps

1. **Update Manifests**

    Update your manifests with your node name and the number of control plane and worker nodes you want to deploy. The default configuration deploys a 3-node control plane and 3-node worker nodes. You can modify the number of nodes by changing the number in the manifest files.

    ```bash
    cd examples/talos
    export NODE_NAME=YOUR_PROXMOX_NODE_NAME
    sed -i "s/\NODE_NAME/$NODE_NAME/g" talos-template.yaml talos-masters.yaml talos-workers.yaml
    ```

2. **Create the Virtual Machine Template**

   Create a new virtual machine template for Talos. This template will be used for deploying the Talos cluster. Apply the manifest with:

   ```bash
   kubectl apply -f talos-template.yaml
   ```

3. **Verify the Template**

   Ensure the virtual machine template is in the `Available` state:

   ```bash
   kubectl get virtualmachinetemplate
   ```

   Example output:
   ```
   NAME             NODE       CORES   MEMORY   IMAGE                             USERNAME   PASSWORD   STATUS      AGE
   talos-template   lowtower   4       4096     talos-1.9.5-with-qemu-amd64.iso                         Available   9m25s
   ```

4. **Deploy the Virtual Machines**

   Create the Virtual Machines for the Talos cluster by running:

   ```bash
   kubectl apply -f talos-masters.yaml
   kubectl apply -f talos-workers.yaml
   ```

5. **Retrieve the Control Plane IP**

   Once the Virtual Machines are created, obtain the IP address of the primary control plane node:

   ```bash
   export CONTROL_PLANE_IP=$(kubectl get vm talos-master-0 -o jsonpath='{.status.status.IPAddress}')
   ```

   **NOTE:** If using floating IPs for the control plane nodes, substitute the IP of the first control plane node with the floating IP as needed.

6. **Generate the Talos Configuration**

   Generate the Talos configuration for the cluster using the QEMU-supported Talos image:

   ```bash
   talosctl gen config talos-proxmox-cluster https://$CONTROL_PLANE_IP:6443 --output-dir talos-proxmox-cluster --install-image factory.talos.dev/installer/ce4c980550dd2ab1b17bbf2b08801c7eb59418eafe8f279833297925d67c7515:v1.9.5
   ```

7. **Retrieve IP Addresses for Control Plane and Worker Nodes**

   Get the IP addresses of the control plane and worker nodes. These commands output comma-separated lists of IP addresses:

   ```bash
   export MASTER_IPS=$(kubectl get vm talos-master-{0..2} -o jsonpath='{.items[*].status.status.IPAddress}' | tr ' ' ',')
   export WORKER_IPS=$(kubectl get vm talos-worker-{0..2} -o jsonpath='{.items[*].status.status.IPAddress}' | tr ' ' ',')
   ```

8. **Apply the Talos Configuration**

   Apply the configuration to the control plane and worker nodes:

   ```bash
   talosctl apply-config --insecure --nodes $MASTER_IPS --file talos-proxmox-cluster/controlplane.yaml
   talosctl apply-config --insecure --nodes $WORKER_IPS --file talos-proxmox-cluster/worker.yaml
   ```

   **NOTE:** Repeat these commands as many times as necessary to cover all control plane and worker nodes.

9. **Bootstrap the Talos Cluster**

   Set the Talos configuration and bootstrap the cluster:

   ```bash
   export TALOSCONFIG="talos-proxmox-cluster/talosconfig"
   talosctl config endpoint $CONTROL_PLANE_IP 
   talosctl config node $CONTROL_PLANE_IP
   talosctl bootstrap
   ```

10. **Retrieve the kubeconfig**

   Once bootstrapped, generate the kubeconfig for the cluster:

   ```bash
   talosctl kubeconfig .
   export KUBECONFIG=$(pwd)/kubeconfig
   ```

11. **Verify the Cluster**

    Check that the cluster is up and running:

    ```bash
    kubectl get nodes
    ```

## Cleanup

To clean up resources, delete the Virtual Machines and the template:

1. **Delete the Virtual Machines:**

   ```bash
   kubectl delete -f talos-masters.yaml
   kubectl delete -f talos-workers.yaml
   ```

2. **Delete the Virtual Machine Template:**

   ```bash
   kubectl delete -f talos-template.yaml
   ```
