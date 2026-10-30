package proxmox

import (
	"fmt"
	"testing"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	uidA         = "uid-a"
	uidB         = "uid-b"
	testTemplate = "tpl"
)

// The live repro: two Container objects with the same spec.name bound one
// container, and deleting either destroyed it.
func TestTwoObjectsWithOneHostname(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{9000: {name: testTemplate, status: VirtualMachineStoppedState}})
	a, b := testContainer(uidA, 0), testContainer(uidB, 0)
	a.Spec.Template.Name, b.Spec.Template.Name = testTemplate, testTemplate

	if _, err := pc.ClaimContainer(a); !IsContainerNotFound(err) {
		t.Fatalf("ClaimContainer(a) before create = %v, want not found", err)
	}
	id, err := pc.CloneContainer(a)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.cts[id].tags; got != "kubemox-uid-"+uidA {
		t.Fatalf("clone tags = %q", got)
	}
	a.Status.Status.ID = id

	if _, err := pc.ClaimContainer(b); !IsNameConflict(err) {
		t.Fatalf("ClaimContainer(b) = %v, want a NameConflictError", err)
	}
	if err := pc.DeleteContainer(b); err != nil {
		t.Fatalf("DeleteContainer(b) = %v, want nil: b has no container", err)
	}
	if f.cts[id] == nil {
		t.Fatal("deleting b destroyed a's container")
	}
	if err := pc.DeleteContainer(a); err != nil || f.cts[id] != nil {
		t.Fatalf("DeleteContainer(a) = %v, container left: %v", err, f.cts[id] != nil)
	}
}

// Before this change the watcher recorded status.status.id from a hostname
// lookup, so two objects of an older kubemox can carry the same VMID. The
// first to reconcile tags the container; the other is refused.
func TestObjectsSharingARecordedVMID(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineStoppedState}})
	a, b := testContainer(uidA, 100), testContainer(uidB, 100)

	if id, err := pc.ClaimContainer(a); err != nil || id != 100 {
		t.Fatalf("ClaimContainer(a) = %d, %v", id, err)
	}
	if got := f.cts[100].tags; got != "kubemox-uid-"+uidA {
		t.Fatalf("tags after adoption = %q", got)
	}
	if _, err := pc.ClaimContainer(b); !IsNameConflict(err) {
		t.Fatalf("ClaimContainer(b) = %v, want a NameConflictError", err)
	}
	if err := pc.DeleteContainer(b); err != nil || f.cts[100] == nil {
		t.Fatalf("DeleteContainer(b) = %v; a's container destroyed: %v", err, f.cts[100] == nil)
	}
}

func TestRecordedVMIDOutlivesARename(t *testing.T) {
	_, pc := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: "renamed", status: VirtualMachineRunningState, tags: "kubemox-uid-" + uidA},
		101: {name: testCTName, status: VirtualMachineRunningState}, // a namesake made by hand
	})

	ct, err := pc.GetContainer(testContainer(uidA, 100))
	if err != nil {
		t.Fatal(err)
	}
	if ct.VMID != 100 {
		t.Fatalf("GetContainer = vmid %d, want 100: the hostname was looked up", ct.VMID)
	}
}

// Proxmox hands a freed VMID out again. An untagged container that took it
// under another hostname is not the object's.
func TestRecycledVMIDIsNotAdopted(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: "someone-else", status: VirtualMachineStoppedState}})
	obj := testContainer(uidA, 100)

	if _, err := pc.GetContainer(obj); !IsContainerNotFound(err) {
		t.Fatalf("GetContainer = %v, want not found", err)
	}
	if err := pc.DeleteContainer(obj); err != nil || f.cts[100] == nil {
		t.Fatalf("DeleteContainer = %v; stranger destroyed: %v", err, f.cts[100] == nil)
	}
}

func TestCloneReplacesTheTemplatesUIDTag(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{
		9000: {name: testTemplate, status: VirtualMachineStoppedState, tags: "base;kubemox-uid-zzz"},
	})
	obj := testContainer(uidA, 0)
	obj.Spec.Template.Name = testTemplate

	id, err := pc.CloneContainer(obj)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.cts[id].tags; got != "base;kubemox-uid-"+uidA {
		t.Fatalf("clone tags = %q", got)
	}
}

func TestOCICreateOptionsCarryTheUIDTag(t *testing.T) {
	obj := testContainer(uidA, 0)
	obj.Spec.Template.Image = &proxmoxv1alpha1.ContainerTemplateImage{Reference: "nginx:1.27", Storage: "local"}
	obj.Spec.Template.Disk = []proxmoxv1alpha1.ContainerTemplateDisk{{Storage: "local-lvm", Size: 4}}

	opts, err := newContainerOptions(obj, "local:vztmpl/nginx-1.27.tar", testCTName)
	if err != nil {
		t.Fatal(err)
	}
	tags := ""
	for _, o := range opts {
		if o.Name == containerOptionTags {
			tags = fmt.Sprint(o.Value)
		}
	}
	if tags != "img-nginx-1-27;kubemox-uid-"+uidA {
		t.Fatalf("tags = %q", tags)
	}
	// The UID tag alone is no image tag, so adopting an OCI container does
	// not make the controller think it runs another image and replace it.
	if LXCHasAnyOCIImageTag("kubemox-uid-" + uidA) {
		t.Fatal("a UID tag counted as an image tag")
	}
}

func TestPickContainerByName(t *testing.T) {
	ct := func(id int, tags string) *proxmox.Container {
		return &proxmox.Container{VMID: proxmox.StringOrUint64(uint64(id)), Tags: tags}
	}
	ours, theirs := "kubemox-uid-"+uidA, "kubemox-uid-"+uidB
	cases := []struct {
		name  string
		named []*proxmox.Container
		want  int
		err   func(error) bool
	}{
		{"none", nil, 0, IsContainerNotFound},
		{"ours", []*proxmox.Container{ct(100, "x;"+ours)}, 100, nil},
		{"ours among others", []*proxmox.Container{ct(100, theirs), ct(101, ours), ct(102, "")}, 101, nil},
		{"one untagged", []*proxmox.Container{ct(100, "")}, 100, nil},
		{"another object's", []*proxmox.Container{ct(100, theirs)}, 0, IsNameConflict},
		{"untagged next to another object's", []*proxmox.Container{ct(100, theirs), ct(101, "")}, 0, IsNameConflict},
		{"two untagged", []*proxmox.Container{ct(100, ""), ct(101, "")}, 0, IsNameConflict},
	}
	for _, tc := range cases {
		id, err := pickContainerByName(testContainer(uidA, 0), tc.named)
		switch {
		case tc.err == nil && (err != nil || id != tc.want):
			t.Errorf("%s: = %d, %v; want %d", tc.name, id, err, tc.want)
		case tc.err != nil && !tc.err(err):
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
}
