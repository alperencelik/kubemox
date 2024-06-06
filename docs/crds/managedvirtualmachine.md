# ManagedVirtualMachine

`ManagedVirtualMachine` is a way to bring your existing VirtualMachines in Proxmox to Kubernetes. As an user you don't need to create `ManagedVirtualMachine` resource. Kubemox will create `ManagedVirtualMachine` objects according to existing VirtualMachines in Proxmox who has a tag called `kubemox-managed-vm` by default. You can change the tag name by setting `MANAGED_VIRTUAL_MACHINE_TAG` environment variable in the controller. `ManagedVirtualMachine` is also reconciled by the operator so if you do any change on those (delete, update, etc.) it will be reflected to Proxmox.

!!! warning
    You should not create `ManagedVirtualMachine` resource manually. Kubemox will create it for you after the deployment at startup of controller. That is way to bring your existing VirtualMachines in Proxmox to Kubernetes. You can delete or modify them but you should not create them manually.
