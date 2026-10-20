package proxmox

import (
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// VMRef identifies one virtual machine on a Proxmox cluster.
//
// Proxmox guarantees uniqueness for the VMID and not for the name: two
// machines, on the same node or on different ones, may share a name. Code that
// resolves a machine by name therefore has to decide what to do when it finds
// more than one, and the only answer that cannot pick a stranger's machine is
// to refuse.
//
// Name is carried alongside ID because it is what a person recognises in a log
// line, and because it is all that exists before a machine has been observed
// once — the first lookup after creation, or adopting a machine that was made
// outside kubemox.
type VMRef struct {
	// ID is the VMID. Zero means "not known yet", not "VMID 0".
	ID int
	// Name is the Proxmox VM name, which is spec.name on the custom resource
	// and is not necessarily the name of the resource itself.
	Name string
	// Node is the Proxmox node the machine lives on.
	Node string
}

// HasID reports whether the reference carries a resolved VMID.
func (r VMRef) HasID() bool { return r.ID > 0 }

func (r VMRef) String() string {
	if r.HasID() {
		return fmt.Sprintf("%s (vmid %d) on %s", r.Name, r.ID, r.Node)
	}
	return fmt.Sprintf("%s on %s", r.Name, r.Node)
}

// VMRefFromCR builds a reference from a VirtualMachine resource.
//
// The VMID comes from status, which the fetcher fills in after the first
// successful observation. Once it is there, every later call addresses the
// machine by ID and a rename in Proxmox no longer detaches the resource from
// the machine it created.
//
// This is also the single place where the mapping from a resource to a Proxmox
// machine is decided. Before it existed, some call sites passed spec.name and
// others passed the resource's own metadata name, which only agreed when a
// user happened to write them the same.
func VMRefFromCR(vm *proxmoxv1alpha1.VirtualMachine) VMRef {
	ref := VMRef{Name: vm.Spec.Name, Node: vm.Spec.NodeName}
	if vm.Status.Status != nil {
		ref.ID = vm.Status.Status.ID
	}
	return ref
}

// NamedVMRef builds a reference for a machine that has not been observed yet,
// or for one being looked up by name on purpose — adoption, templates, and the
// clone source.
func NamedVMRef(name, node string) VMRef {
	return VMRef{Name: name, Node: node}
}
