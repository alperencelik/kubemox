package proxmox

import (
	"errors"
	"testing"
)

const testCTName = "web"

// assertContainerGone fails unless err is the NotFoundError the controller
// reads as "finish the deletion".
func assertContainerGone(t *testing.T, err error) {
	t.Helper()
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("DeleteContainer = %v, want a NotFoundError so the finalizer goes", err)
	}
}

func TestDeleteContainer_NoSuchName_ReturnsNotFound(t *testing.T) {
	_, pc := newFakeLXCHost(t, map[int]*fakeCT{})

	assertContainerGone(t, pc.DeleteContainer(testCTName, lxcTestNode))
}

func TestDeleteContainer_GoneBehindTheCache_ReturnsNotFound(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	// Someone runs `pct destroy 100`; kubemox still caches the running CT.
	delete(f.cts, 100)

	assertContainerGone(t, pc.DeleteContainer(testCTName, lxcTestNode))
	if _, ok := pc.getCachedContainerID(lxcTestNode, testCTName); ok {
		t.Error("the VMID of the deleted container is still cached")
	}
	if pc.getCachedContainer(lxcTestNode, 100) != nil {
		t.Error("the deleted container object is still cached")
	}
}

func TestDeleteContainer_VMIDReused_LeavesTheOtherContainerAlone(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	// CT 100 is destroyed outside kubemox and Proxmox hands the VMID out again.
	f.cts[100] = &fakeCT{name: "other", status: VirtualMachineRunningState}

	assertContainerGone(t, pc.DeleteContainer(testCTName, lxcTestNode))
	if f.called("POST /api2/json/nodes/" + lxcTestNode + "/lxc/100/status/stop") {
		t.Error("another container was stopped")
	}
	if _, ok := f.cts[100]; !ok {
		t.Error("another container was destroyed")
	}
	if _, ok := pc.getCachedContainerID(lxcTestNode, testCTName); ok {
		t.Error("the stale VMID is still cached")
	}
}

func TestDeleteContainer_VMIDReusedByStoppedCT_LeavesTheOtherContainerAlone(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	f.cts[100] = &fakeCT{name: "other", status: VirtualMachineStoppedState, pingErr: "CT 100 not running"}

	assertContainerGone(t, pc.DeleteContainer(testCTName, lxcTestNode))
	if _, ok := f.cts[100]; !ok {
		t.Error("another container was destroyed")
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

func TestDeleteContainer_NotRunningAnswer_StillDestroys(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineStoppedState, pingErr: "CT 100 not running"},
	})

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.cts[100]; ok {
		t.Error("the stopped container still exists")
	}
}

func TestDeleteContainer_DestroyRefused_ReturnsError(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineStoppedState, lockErr: "CT is locked (backup)"},
	})

	if err := pc.DeleteContainer(testCTName, lxcTestNode); err == nil {
		t.Fatal("DeleteContainer = nil although Proxmox refused, so the finalizer would go and the CT stay")
	}
	if _, ok := f.cts[100]; !ok {
		t.Fatal("the fake destroyed a locked container")
	}
}

func TestDeleteContainer_TaskFails_ReturnsTaskError(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineStoppedState}})
	f.taskExit = "command 'pct destroy' failed"

	err := pc.DeleteContainer(testCTName, lxcTestNode)
	var taskErr *TaskError
	if !errors.As(err, &taskErr) {
		t.Fatalf("DeleteContainer = %v, want a TaskError", err)
	}
}
