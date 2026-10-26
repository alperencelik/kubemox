package proxmox

import (
	"testing"
)

const testCTName = "web"

// assertMutexFree fails when DeleteContainer left the package mutex locked,
// which would block every later VM clone, VM create and container delete.
func assertMutexFree(t *testing.T) {
	t.Helper()
	if !mutex.TryLock() {
		t.Fatal("DeleteContainer returned with the package mutex still locked")
	}
	mutex.Unlock()
}

func TestDeleteContainer_NoSuchName_CountsAsDeleted(t *testing.T) {
	_, pc := newFakeLXCHost(t, map[int]*fakeCT{})

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err != nil {
		t.Fatalf("DeleteContainer of a missing container = %v, want nil so the finalizer goes", err)
	}
	assertMutexFree(t)
}

func TestDeleteContainer_GoneBehindTheCache_CountsAsDeleted(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	// Someone runs `pct destroy 100`; kubemox still caches the running CT.
	delete(f.cts, 100)

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err != nil {
		t.Fatalf("DeleteContainer = %v, want nil", err)
	}
	assertMutexFree(t)
	if _, ok := pc.getCachedContainerID(lxcTestNode, testCTName); ok {
		t.Error("the VMID of the deleted container is still cached")
	}
	if pc.getCachedContainer(lxcTestNode, 100) != nil {
		t.Error("the deleted container object is still cached")
	}
}

func TestDeleteContainer_StopsThenDestroys(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	assertMutexFree(t)
	if !f.called("POST /api2/json/nodes/" + lxcTestNode + "/lxc/100/status/stop") {
		t.Error("the running container was not stopped first")
	}
	if _, ok := f.cts[100]; ok {
		t.Error("the container still exists")
	}
	if pc.getCachedContainer(lxcTestNode, 100) != nil {
		t.Error("the deleted container object is still cached")
	}
}

func TestDeleteContainer_DestroyRefused_ReturnsErrorAndUnlocks(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineStoppedState, lockErr: "CT is locked (backup)"},
	})

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err == nil {
		t.Fatal("DeleteContainer = nil although Proxmox refused, so the finalizer would go and the CT stay")
	}
	assertMutexFree(t)
	if _, ok := f.cts[100]; !ok {
		t.Fatal("the fake destroyed a locked container")
	}
}
