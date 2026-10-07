package proxmox

import (
	"errors"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
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

	vmID, err := pc.getVMID(NamedVMRef("no-such-vm", "pve1"))
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

// TestVMRefFromCR_UsesSpecNameAndObservedID pins which of the two names on a
// VirtualMachine resource is the Proxmox one.
//
// They are independent: spec.name is required and nothing defaults it to the
// resource's own name. Several call sites used to pass metadata.name, so a
// resource named "db" holding spec.name "database" had its network, disk and
// PCI configuration read from whatever machine happened to be called "db".
func TestVMRefFromCR_UsesSpecNameAndObservedID(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "team-a"},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name:     "database",
			NodeName: "pve1",
		},
	}

	ref := VMRefFromCR(vm)
	if ref.Name != "database" {
		t.Fatalf("VMRefFromCR().Name = %q, want the Proxmox name from spec", ref.Name)
	}
	if ref.HasID() {
		t.Fatalf("VMRefFromCR().ID = %d before the machine was observed, want zero", ref.ID)
	}

	vm.Status.Status = &proxmoxv1alpha1.QEMUStatus{ID: 142}
	if ref = VMRefFromCR(vm); ref.ID != 142 {
		t.Fatalf("VMRefFromCR().ID = %d after observation, want 142", ref.ID)
	}
}

// TestGetVMID_KnownID_SkipsNameLookup is the whole point of the change: once a
// machine has been observed, kubemox stops asking Proxmox what "web" means.
//
// The assertion is on the request count rather than the return value, because
// returning 100 would also happen by accident if the name were resolved.
func TestGetVMID_KnownID_SkipsNameLookup(t *testing.T) {
	f := newFakeProxmox(t, map[string][]fakeVM{
		"pve1": {{VMID: 100, Name: "renamed-in-proxmox", Status: "running"}},
	})
	pc := f.client()

	// The reference still carries the name the machine had when it was created.
	ref := VMRef{ID: 100, Name: "web", Node: "pve1"}
	vmID, err := pc.getVMID(ref)
	if err != nil {
		t.Fatalf("getVMID: %v", err)
	}
	if vmID != 100 {
		t.Fatalf("getVMID = %d, want 100", vmID)
	}
	if n := f.requests["/api2/json/nodes/pve1/qemu"]; n != 0 {
		t.Fatalf("listed VMs %d time(s) for a reference that already had an ID", n)
	}
}

// TestGetVMID_NameLookup_Adopts covers the other half: a machine that has
// never been observed, or was created outside kubemox, is still found by name.
func TestGetVMID_NameLookup_Adopts(t *testing.T) {
	f := newFakeProxmox(t, map[string][]fakeVM{
		"pve1": {{VMID: 100, Name: "web", Status: "running"}},
	})
	pc := f.client()

	vmID, err := pc.getVMID(NamedVMRef("web", "pve1"))
	if err != nil {
		t.Fatalf("getVMID: %v", err)
	}
	if vmID != 100 {
		t.Fatalf("getVMID = %d, want 100", vmID)
	}
}

// TestGetVMID_NameLookup_IsNotCachedAcrossRenames is a regression test for a
// live failure, and the reason the name-to-VMID cache is gone.
//
// Two tenants each declared spec.name "web". The first adopted vmid 100 and
// the lookup cached web -> 100. The machine was then renamed in Proxmox, so no
// machine called "web" existed any more — but the second tenant's reconcile
// read the stale entry, logged "VirtualMachine web already exists", and
// started reconfiguring the first tenant's machine.
//
// A name-to-VMID map has no way to learn about a rename, so the only safe
// cache is none. It costs one API call per resource, since a name is resolved
// once and the VMID is carried in status from then on.
func TestGetVMID_NameLookup_IsNotCachedAcrossRenames(t *testing.T) {
	f := newFakeProxmox(t, map[string][]fakeVM{
		"pve1": {{VMID: 100, Name: "web", Status: "running"}},
	})
	pc := f.client()

	if _, err := pc.getVMID(NamedVMRef("web", "pve1")); err != nil {
		t.Fatalf("first lookup: %v", err)
	}

	f.rename("pve1", 100, "renamed-in-proxmox")

	vmID, err := pc.getVMID(NamedVMRef("web", "pve1"))
	if err == nil {
		t.Fatalf("second lookup of \"web\" returned vmid %d after the machine was renamed; "+
			"want not-found, since answering with 100 hands over another tenant's machine", vmID)
	}
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("second lookup error = %v, want NotFoundError", err)
	}
}
