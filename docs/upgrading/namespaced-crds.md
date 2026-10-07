# Upgrade to namespaced CRDs

This guide covers the release that moves `VirtualMachine`, `VirtualMachineSet`, `VirtualMachineTemplate`, `Container`, `CustomCertificate` and `StorageDownloadURL` from `Cluster` scope back to `Namespaced`.

`ProxmoxConnection` stays cluster-scoped.

!!! warning
    `spec.scope` is immutable on an existing CustomResourceDefinition. The CRDs have to be deleted and recreated, and **objects do not survive that**. This is a migration, not an upgrade: what you do not export beforehand is gone.

## Why this reverses v0.3.0

[v0.3.0](v0.3.0.md) moved these CRDs the other way, from `Namespaced` to `Cluster`. Cluster scope makes the object name globally unique, which is convenient when one team runs one Proxmox cluster.

It does not hold up with more than one tenant:

- two teams cannot both have a `VirtualMachine` called `web`, and the second one gets an error about an object it cannot see;
- a Role cannot be scoped to a team's own machines, because there is no namespace to scope it to;
- a namespaced Helm release cannot render a cluster-scoped object per tenant without the tenants colliding.

## Before you start

Your virtual machines are not touched by any of this — the objects that describe them are. Deleting a `VirtualMachine` resource while the operator is running **will** delete the machine, which is why the operator is scaled down first and the finalizers are removed by hand.

## Migration steps

* Export every Kubemox object.

```shell
for kind in virtualmachines virtualmachinesets virtualmachinetemplates containers customcertificates storagedownloadurls; do
  kubectl get "$kind" -o yaml > "kubemox-backup-${kind}.yaml"
done
```

* Scale the operator down, so that nothing reconciles while the objects are gone.

```shell
kubectl scale deployment kubemox --replicas=0
```

* Confirm it is actually down before continuing.

```shell
kubectl get pods -l app.kubernetes.io/name=kubemox
```

* Remove the finalizers from the exported objects, then delete them. With the operator stopped nothing will clean up the Proxmox side, which is the point — the machines have to outlive their objects.

```shell
for kind in virtualmachines virtualmachinesets virtualmachinetemplates containers customcertificates storagedownloadurls; do
  for name in $(kubectl get "$kind" -o name); do
    kubectl patch "$name" --type=merge -p '{"metadata":{"finalizers":null}}'
  done
  kubectl delete "$kind" --all
done
```

* Delete the CustomResourceDefinitions themselves.

```shell
kubectl delete crd \
  virtualmachines.proxmox.alperen.cloud \
  virtualmachinesets.proxmox.alperen.cloud \
  virtualmachinetemplates.proxmox.alperen.cloud \
  containers.proxmox.alperen.cloud \
  customcertificates.proxmox.alperen.cloud \
  storagedownloadurls.proxmox.alperen.cloud
```

* Upgrade the chart, which installs the CRDs with the new scope.

```shell
helm upgrade kubemox /path/to/kubemox/helm/chart
```

* Add a namespace to every exported object and re-apply it. Anything without a namespace will land in whatever namespace `kubectl` is pointed at, so set it explicitly.

```shell
for kind in virtualmachines virtualmachinesets virtualmachinetemplates containers customcertificates storagedownloadurls; do
  kubectl apply -n <your-namespace> -f "kubemox-backup-${kind}.yaml"
done
```

* Scale the operator back up.

```shell
kubectl scale deployment kubemox --replicas=1
```

## Adoption, not recreation

The restored objects describe machines that already exist. The operator finds each one by `spec.name` on the node in `spec.nodeName`, records its VMID in `status`, and manages it from there — nothing is cloned or recreated.

The one case this cannot handle is a `spec.name` that matches more than one machine on the cluster. The operator refuses rather than picking, so if a restore reports an ambiguous name, rename one of the machines in Proxmox before retrying.
