// Package v1alpha1 contains the external (versioned) API types for the
// ops.proxmox.alperen.cloud API group. These types are served by the
// kubemox aggregated apiserver and project live Proxmox state and
// imperative operations onto the Kubernetes API surface.
//
// # Why a separate API group
//
// proxmox.alperen.cloud holds the declarative CRDs (desired state, stored
// in etcd, reconciled by controllers). ops.proxmox.alperen.cloud is served
// by the aggregated apiserver and holds nothing in etcd — every response
// is a live projection of Proxmox, and write verbs trigger imperative
// Proxmox operations. The two groups intentionally share resource names
// (VirtualMachine, Node) because they describe the same conceptual object
// from two different angles: declared vs. live/operational.
//
// # Resources
//
// All resources in this group are cluster-scoped — no namespace flag is
// needed on kubectl.
//
//	virtualmachines       Live projection of every kubemox VirtualMachine CR
//	  /metrics            Point-in-time CPU/memory/disk/net sample (GET)
//	  /start              Power on (POST, async — returns TaskRef)
//	  /stop               Hard stop, optional force (POST, async)
//	  /reboot             Reset (POST, async)
//	  /shutdown           ACPI shutdown, optional timeout (POST, async)
//	nodes                 Live projection of every Proxmox node across all connections
//	tasks                 Live projection of a Proxmox task by UPID
//
// # kubectl usage
//
// Reading state:
//
//	# List all VMs known to kubemox, with their live Proxmox status.
//	kubectl get virtualmachines.ops.proxmox.alperen.cloud
//	kubectl get vm.ops.proxmox.alperen.cloud my-vm -o yaml
//
//	# List Proxmox nodes across every ProxmoxConnection.
//	kubectl get nodes.ops.proxmox.alperen.cloud
//
//	# Poll a Proxmox task by its UPID (returned in TaskRef from async verbs).
//	kubectl get tasks.ops.proxmox.alperen.cloud UPID:pve01:...
//
// Imperative verbs are POST-on-subresource. kubectl doesn't have a
// first-class verb for that, so pipe the JSON body into `kubectl create
// --raw ... -f -`. The body must always carry at least apiVersion + kind
// so the apiserver's decoder can construct the object — there is no
// "empty body" form.
//
//	# Power on a VM (no options needed, but the envelope is required).
//	echo '{"apiVersion":"ops.proxmox.alperen.cloud/v1alpha1","kind":"PowerAction"}' | kubectl create --raw /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/my-vm/start -f -
//
//	# Force-stop a VM (kills it instead of ACPI shutdown).
//	echo '{"apiVersion":"ops.proxmox.alperen.cloud/v1alpha1","kind":"PowerAction","forceStop":true}' | kubectl create --raw /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/my-vm/stop -f -
//
//	# Graceful ACPI shutdown with a 60s timeout.
//	echo '{"apiVersion":"ops.proxmox.alperen.cloud/v1alpha1","kind":"PowerAction","timeoutSeconds":60}' | kubectl create --raw /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/my-vm/shutdown -f -
//
//	# Reboot.
//	echo '{"apiVersion":"ops.proxmox.alperen.cloud/v1alpha1","kind":"PowerAction"}' | kubectl create --raw /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/my-vm/reboot -f -
//
// Async verbs return a PowerAction whose TaskRef carries the Proxmox
// UPID. Poll it with:
//
//	kubectl get tasks.ops.proxmox.alperen.cloud <UPID> -o yaml
//
// Read-only subresources use GET, which kubectl exposes as `--raw`:
//
//	# Live metrics sample.
//	kubectl get --raw \
//	  /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines/my-vm/metrics
//
// +kubebuilder:object:generate=true
// +k8s:openapi-gen=true
// +groupName=ops.proxmox.alperen.cloud
package v1alpha1
