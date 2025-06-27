# Creating your First Proxmox Resource

Proxmox resources are defined using Custom Resource Definitions (CRDs) in Kubemox. This guide will help you create your first Proxmox resource.

# Creating the ProxmoxConnection

`Kubemox` supports users to maintain resources in multiple Proxmox clusters at the same time. For this purpose, you need to create a `ProxmoxConnection` resource where you define your Proxmox cluster connection details. This resource is essential for the operator to connect to your Proxmox cluster and all other resources will reference this connection.

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: ProxmoxConnection
metadata:
  name: proxmox-connection-sample
spec:
  endpoint: "PROXMOX_ENDPOINT"
  username: "PROXMOX_USERNAME"
  password: "PROXMOX_PASSWORD"
  insecureSkipVerify: true
EOF
```

For more information about the fields, you can check the [ProxmoxConnection documentation](https://alperencelik.github.io/kubemox/crds/proxmoxconnection/).

## Referencing your ProxmoxConnection

`ProxmoxConnection` resource itself does not create any Proxmox resources. It is used to create other Proxmox resources such as `VirtualMachine`, `Container`, and `Storage`. You can reference the `ProxmoxConnection` resource in the spec of these resources. It's a metadata object to be referenced in other resources. For example, you can reference the `ProxmoxConnection` on resources under `spec.connectionRef.name` field. For more information, you can check the [ProxmoxConnection documentation](https://alperencelik.github.io/kubemox/crds/virtualmachine/)
