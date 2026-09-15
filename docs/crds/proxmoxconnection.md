# ProxmoxConnection

`ProxmoxConnection` is a resource that provides a connection to a Proxmox server. It allows the operator to interact with the Proxmox API and perform operations on Proxmox resources. The `ProxmoxConnection` resource is used to store the connection details, such as the Proxmox endpoint, username, and password.

## Creating ProxmoxConnection

To create a new `ProxmoxConnection` resource, you need to provide the connection details in the manifest. Here is an example of how to create a `ProxmoxConnection` resource:

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

In the above example, replace `PROXMOX_ENDPOINT`, `PROXMOX_USERNAME`, and `PROXMOX_PASSWORD` with your Proxmox server's endpoint, username, and password. The `insecureSkipVerify` field is optional and can be set to `true` if you want to skip SSL verification. For more information about the fields, you can check the examples/ directory.

## Reading credentials from a Secret

Instead of writing the password or the API token secret into the `ProxmoxConnection`, you can reference a key of a Secret with `passwordFrom` or `secretFrom`. `ProxmoxConnection` is cluster-scoped, so the reference always names the Secret's namespace.

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: proxmox-credentials
  namespace: kubemox
stringData:
  token: "PROXMOX_TOKEN_SECRET"
---
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: ProxmoxConnection
metadata:
  name: proxmox-connection-sample
spec:
  endpoint: "PROXMOX_ENDPOINT"
  tokenID: "root@pam!kubemox"
  secretFrom:
    name: proxmox-credentials
    namespace: kubemox
    key: token
  insecureSkipVerify: true
EOF
```

For username authentication use `username` with `passwordFrom` in the same way. Each credential takes exactly one source: setting both `password` and `passwordFrom`, or `secret` and `secretFrom`, is rejected when the object is created.

**Permissions.** kubemox reads these Secrets with `get` only. The Helm chart creates a Role granting that in each namespace listed in `rbac.secretNamespaces`, and in the release namespace when the list is empty - so keep the Secrets there, or list their namespace. The kustomize manifests in `config/rbac` grant `get` on secrets cluster-wide, since a generated role cannot name namespaces chosen at install time.

**Rotation.** A client built from a Secret is rebuilt at most five minutes after the Secret changes, without touching the `ProxmoxConnection`. The connection's `Ready` condition is re-evaluated when its spec changes, so after rotating a Secret it may show the previous result until then.

## Referencing your ProxmoxConnection

`ProxmoxConnection` resource itself does not create any Proxmox resources. It is used to create other Proxmox resources such as `VirtualMachine`, `Container`, and `Storage`. You can reference the `ProxmoxConnection` resource in the spec of these resources. It's a metadata object to be referenced in other resources. For example, you can reference the `ProxmoxConnection` resource in the `VirtualMachine` resource as follows:

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone-new
  namespace: default
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-clone-new
  nodeName: lowtower
  template:
    socket: 1
    cores: 2
    disk:
    - device: scsi0
      size: 60
      storage: local-lvm
    memory: 4096
```
  