package proxmox

import (
	"sort"
	"testing"
)

// twoNodesSharingAName is the shape that matters: two different machines, on
// two different nodes, that a human gave the same name. Proxmox allows it -
// identity there is the VMID, not the name - so kubemox has to survive it.
func twoNodesSharingAName() map[string][]fakeVM {
	return map[string][]fakeVM{
		"pve1": {{VMID: 100, Name: "web", Status: "running"}},
		"pve2": {{VMID: 200, Name: "web", Status: "running"}},
	}
}

// TestFakeProxmox_ServesInventory is a check on the harness, not on kubemox.
// If this fails, the two tests below are failing for the wrong reason.
func TestFakeProxmox_ServesInventory(t *testing.T) {
	pc := newFakeProxmox(t, twoNodesSharingAName()).client()

	nodes, err := pc.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	sort.Strings(nodes)
	if len(nodes) != 2 || nodes[0] != "pve1" || nodes[1] != "pve2" {
		t.Fatalf("GetNodes = %v, want [pve1 pve2]", nodes)
	}
}

// TestGetVMID_NotFound_ReturnsError pins the contract that a lookup which
// found nothing says so.
//
// Today getVMID ends with `return 0, nil`, so "no such VM" and "the VM has ID
// 0" are the same answer. Every caller that treats a nil error as success then
// proceeds with a zero VMID.
func TestGetVMID_NotFound_ReturnsError(t *testing.T) {
	pc := newFakeProxmox(t, map[string][]fakeVM{
		"pve1": {{VMID: 100, Name: "web", Status: "running"}},
	}).client()

	vmID, err := pc.getVMID("no-such-vm", "pve1")
	if err == nil {
		t.Fatalf("getVMID(no-such-vm) = (%d, nil), want an error saying it was not found", vmID)
	}
}

// TestGetNodeOfVM_AmbiguousName_ReturnsError pins the collision itself.
//
// GetNodeOfVM scans every online node and returns the first case-insensitive
// name match. With two machines called "web" the answer depends on map
// iteration order, and the caller is then given one of two different machines
// with no indication that a choice was made.
//
// This is what makes namespacing the CRDs unsafe on its own: two tenants each
// asking for spec.name "web" would land here, and the second reconcile would
// start managing the first tenant's machine.
func TestGetNodeOfVM_AmbiguousName_ReturnsError(t *testing.T) {
	pc := newFakeProxmox(t, twoNodesSharingAName()).client()

	node, err := pc.GetNodeOfVM("web")
	if err == nil {
		t.Fatalf("GetNodeOfVM(web) = (%q, nil) with two machines named web; "+
			"want an error rather than a silent pick", node)
	}
}
