# Reconciliation Mode

Kubemox is also supports different type of reconciliation modes to manage your `Kubemox` resources. You can define the annotation `proxmox.alperen.cloud/reconciliation-mode` in the `kubemox` objects to set the reconciliation mode. The default value is `Reconcile` which means the operator will reconcile the VirtualMachine object and handle the operations on Proxmox VE. You can find the all reconciliation modes in the table below.

| Reconciliation Mode | Description |
|---------------------|-------------|
| Reconcile           | The operator will reconcile the VirtualMachine object and handle the operations on Proxmox VE. |
| WatchOnly              | The operator will only watch the VirtualMachine object and do not handle the operations on Proxmox VE. |
| EnsureExists         | The operator will ensure the VirtualMachine exists on Proxmox VE. If the VirtualMachine does not exist, the operator will create it and do nothing further. |
| Disable              | The operator will not reconcile the VirtualMachine object and do not handle the operations on Proxmox VE. This is useful if you would like to debug something or you would like to disable the operator for the specific VirtualMachine object for a while. |
| DryRun (Experimental)             | The operator will not handle the operations on Proxmox VE. This is useful if you would like to test the VirtualMachine object without affecting the Proxmox VE. |
