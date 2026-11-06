package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const tenantB = "team-b"

// fakeProxmox serves two guests: "web" (vmid 100) on pve1, which belongs to
// another tenant, and "b-web" (vmid 200) on pve2. It records every path asked.
func fakeProxmox(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.Method+" "+req.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := strings.TrimPrefix(req.URL.Path, "/api2/json")
		body := `{"data":{}}`
		switch {
		case p == "/nodes/pve1/qemu":
			body = `{"data":[{"vmid":100,"name":"web"}]}`
		case p == "/nodes/pve2/qemu":
			body = `{"data":[{"vmid":200,"name":"b-web"}]}`
		case p == "/nodes/pve2/qemu/200/status/current":
			body = `{"data":{"vmid":200,"name":"b-web"}}`
		case strings.HasSuffix(p, "/snapshot"):
			body = `{"data":[{"name":"snap1"}]}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func snapshotTestVM(ns, name, guest, node, conn string, created time.Time) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(created)},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{Name: guest, NodeName: node,
			ConnectionRef: &corev1.LocalObjectReference{Name: conn}},
	}
}

func snapshotTestSnapshot(ns, vmName, conn string) *proxmoxv1alpha1.VirtualMachineSnapshot {
	return &proxmoxv1alpha1.VirtualMachineSnapshot{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "snap"},
		Spec: proxmoxv1alpha1.VirtualMachineSnapshotSpec{VirtualMachineName: vmName, SnapshotName: "snap1",
			ConnectionRef: &corev1.LocalObjectReference{Name: conn}},
	}
}

func TestVirtualMachineSnapshotTarget(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	srv, paths := fakeProxmox(t)
	const conn = "snapshot-test-conn"
	connection := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: conn},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{Endpoint: srv.URL + "/api2/json",
			TokenID: "root@pam!test", Secret: "secret"},
	}
	now := time.Now().Truncate(time.Second)
	ctx := context.Background()

	reconcileSnapshot := func(objs ...client.Object) (reconcile.Result, *proxmoxv1alpha1.VirtualMachineSnapshot) {
		t.Helper()
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, connection)...).
			WithStatusSubresource(&proxmoxv1alpha1.VirtualMachineSnapshot{}).Build()
		r := &VirtualMachineSnapshotReconciler{Client: c, Scheme: scheme}
		key := client.ObjectKey{Namespace: tenantB, Name: "snap"}
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		if err != nil {
			t.Fatal(err)
		}
		got := &proxmoxv1alpha1.VirtualMachineSnapshot{}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		return res, got
	}

	// The VM object is called "web" but manages "b-web" on pve2. The snapshot
	// must go to vmid 200, never to the other tenant's "web" (vmid 100).
	vm := snapshotTestVM(tenantB, "web", "b-web", "pve2", conn, now)
	_, snap := reconcileSnapshot(vm, snapshotTestSnapshot(tenantB, "web", conn))
	if snap.Status.Status != snapshotCreatedStatus {
		t.Errorf("status = %q, want %q (error %q)", snap.Status.Status, snapshotCreatedStatus, snap.Status.ErrorMessage)
	}
	var sawTarget bool
	for _, p := range paths() {
		if strings.Contains(p, "/qemu/100") || strings.Contains(p, "/nodes/pve1/") {
			t.Errorf("touched the other tenant's guest: %s", p)
		}
		sawTarget = sawTarget || strings.HasSuffix(p, "/nodes/pve2/qemu/200/snapshot")
	}
	if !sawTarget {
		t.Errorf("never listed snapshots of vmid 200 on pve2; requests: %v", paths())
	}

	// A connectionRef other than the VM's is refused before Proxmox is asked.
	before := len(paths())
	_, snap = reconcileSnapshot(vm, snapshotTestSnapshot(tenantB, "web", "another-conn"))
	if snap.Status.ErrorMessage == "" || snap.Status.Status == snapshotCreatedStatus {
		t.Errorf("mismatched connection: status %q, error %q", snap.Status.Status, snap.Status.ErrorMessage)
	}
	if len(paths()) != before {
		t.Errorf("mismatched connection reached Proxmox: %v", paths()[before:])
	}

	// A VM that lost its spec.name to an older one waits and isn't snapshotted.
	older := snapshotTestVM("team-a", "first", "b-web", "pve2", conn, now.Add(-time.Minute))
	res, snap := reconcileSnapshot(vm, older, snapshotTestSnapshot(tenantB, "web", conn))
	if res.RequeueAfter != nameConflictRequeue || snap.Status.Status == snapshotCreatedStatus {
		t.Errorf("name owned by another VM: requeue %v, status %q", res.RequeueAfter, snap.Status.Status)
	}
	if len(paths()) != before {
		t.Errorf("VM without its name reached Proxmox: %v", paths()[before:])
	}
}

func TestSnapshotPolicyStaysInItsNamespace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	labelled := func(vm *proxmoxv1alpha1.VirtualMachine) *proxmoxv1alpha1.VirtualMachine {
		vm.Labels = map[string]string{"backup": "yes"}
		return vm
	}
	now := time.Now()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		labelled(snapshotTestVM(tenantB, "mine", "mine", "pve1", "pve", now)),
		labelled(snapshotTestVM(tenantB, "elsewhere", "elsewhere", "pve1", "other-pve", now)),
		labelled(snapshotTestVM("team-a", "theirs", "theirs", "pve1", "pve", now)),
	).Build()
	policy := &proxmoxv1alpha1.VirtualMachineSnapshotPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: tenantB, Name: "nightly"},
		Spec: proxmoxv1alpha1.VirtualMachineSnapshotPolicySpec{
			LabelSelector: metav1.LabelSelector{MatchLabels: map[string]string{"backup": "yes"}},
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"},
		},
	}
	r := &VirtualMachineSnapshotPolicyReconciler{Client: c, Scheme: scheme}

	got := GetMatchingVirtualMachines(policy, r, context.Background())
	if len(got) != 1 || got[0].Namespace != tenantB || got[0].Name != "mine" {
		names := make([]string, 0, len(got))
		for _, vm := range got {
			names = append(names, vm.Namespace+"/"+vm.Name)
		}
		t.Errorf("matched %v, want only team-b/mine", names)
	}
}
