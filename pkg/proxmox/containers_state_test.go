package proxmox

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestGetContainer_ReadsCurrentState(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	obj := testContainer("a", 100)
	if ct, err := pc.GetContainer(obj); err != nil || ct.Status != VirtualMachineRunningState {
		t.Fatalf("GetContainer = %+v, %v", ct, err)
	}

	f.cts[100].status = VirtualMachineStoppedState // `pct stop 100`

	ct, err := pc.GetContainer(obj)
	if err != nil {
		t.Fatal(err)
	}
	if ct.Status != VirtualMachineStoppedState {
		t.Fatalf("GetContainer status = %q after the CT stopped, want %q", ct.Status, VirtualMachineStoppedState)
	}
}

func TestContainerExists_FollowsDestroyAndRecreate(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	obj := testContainer("a", 0) // not recorded yet: looked up by hostname
	if ok, err := pc.ContainerExists(obj); err != nil || !ok {
		t.Fatalf("ContainerExists = %v, %v; want true", ok, err)
	}

	delete(f.cts, 100) // `pct destroy 100`
	if ok, err := pc.ContainerExists(obj); err != nil || ok {
		t.Fatalf("ContainerExists after destroy = %v, %v; want false, nil", ok, err)
	}

	f.cts[101] = &fakeCT{name: testCTName, status: VirtualMachineStoppedState} // created again by hand
	ct, err := pc.GetContainer(obj)
	if err != nil {
		t.Fatal(err)
	}
	if ct.VMID != 101 {
		t.Fatalf("GetContainer VMID = %d, want 101: the destroyed VMID is still cached", ct.VMID)
	}
}

// The watcher keeps one client per ProxmoxConnection for as long as the
// connection is unchanged, so each poll has to reach Proxmox.
func TestContainerFetcher_SeesChangesBetweenPolls(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineRunningState, cores: 2, memory: 512},
	})
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "lxc-fresh-state", ResourceVersion: "1"},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: f.url, TokenID: "test@pve!t", Secret: "secret",
		},
	}
	ct := testContainer("a", 100)
	ct.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: conn.Name}
	fetcher := &ContainerFetcher{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn, ct).Build()}
	key := client.ObjectKeyFromObject(ct)
	poll := func() ContainerDesiredState {
		t.Helper()
		raw, err := fetcher.FetchExternalResource(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := fetcher.TransformExternalState(raw)
		if err != nil {
			t.Fatal(err)
		}
		return actual.(ContainerDesiredState)
	}

	if got := poll(); got.Cores != 2 || got.Memory != 512 {
		t.Fatalf("first poll = %+v", got)
	}
	f.cts[100].cores = 4 // `pct set 100 --cores 4`
	if got := poll(); got.Cores != 4 {
		t.Fatalf("second poll = %+v, want cores 4: the watcher compares against a cached object", got)
	}
}
