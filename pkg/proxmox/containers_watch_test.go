package proxmox

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alperencelik/kube-external-watcher/watcher"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// TestContainerWatcher_Drift runs one watcher poll against the fake Proxmox for
// each case, in the order the watcher runs it: desired state, fetch, status
// update, transform, compare.
func TestContainerWatcher_Drift(t *testing.T) {
	cases := []struct {
		name      string
		autoStart bool
		ct        *fakeCT
		drift     bool
	}{
		{"running as asked", true, &fakeCT{status: VirtualMachineRunningState, cores: 2, memory: 512}, false},
		{"stopped with autostart", true, &fakeCT{status: VirtualMachineStoppedState, cores: 2, memory: 512}, true},
		{"stopped without autostart", false, &fakeCT{status: VirtualMachineStoppedState, cores: 2, memory: 512}, false},
		{"running without autostart", false, &fakeCT{status: VirtualMachineRunningState, cores: 2, memory: 512}, false},
		{"resized", true, &fakeCT{status: VirtualMachineRunningState, cores: 4, memory: 512}, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.ct.name = testCTName
			f, _ := newFakeLXCHost(t, map[int]*fakeCT{100: tc.ct})
			fetcher, key, rkey := containerWatcherFixture(t, f, fmt.Sprintf("lxc-drift-%d", i), tc.autoStart)

			desired, err := fetcher.GetDesiredState(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := fetcher.FetchExternalResource(context.Background(), rkey)
			if err != nil {
				t.Fatalf("FetchExternalResource = %v", err)
			}
			if err = fetcher.UpdateResourceStatus(context.Background(), key, raw); err != nil {
				t.Fatalf("UpdateResourceStatus = %v", err)
			}
			actual, err := fetcher.TransformExternalState(raw)
			if err != nil {
				t.Fatal(err)
			}
			comparator := RunningStateComparator()
			drift, err := comparator.HasDrifted(desired, actual)
			if err != nil {
				t.Fatal(err)
			}
			if drift != tc.drift {
				t.Fatalf("drift = %v, want %v; diff (-desired +actual):\n%s", drift, tc.drift, comparator.Diff(desired, actual))
			}
		})
	}
}

// assertRemoved fails unless err is what makes the watcher trigger a reconcile
// for a removed container.
func assertRemoved(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, watcher.ErrExternalResourceNotFound) {
		t.Fatalf("FetchExternalResource = %v, want ErrExternalResourceNotFound", err)
	}
}

func TestContainerFetcher_NoSuchContainer_ReportsRemoval(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{})
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-no-such", true)

	_, err := fetcher.FetchExternalResource(context.Background(), rkey)
	assertRemoved(t, err)
}

func TestContainerFetcher_VMWithTheSameName_StillReportsRemoval(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{})
	f.vmNames = []string{testCTName}
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-vm-name", true)

	_, err := fetcher.FetchExternalResource(context.Background(), rkey)
	assertRemoved(t, err)
}

// A container that moved still exists, and a reconcile would clone a second one.
func TestContainerFetcher_MigratedContainer_IsNotARemoval(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-migrated", true)
	pc, err := NewProxmoxClientFromRef(context.Background(), fetcher.Client, rkey.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	delete(f.cts, 100) // `pct migrate 100 pve-other`
	f.otherNodes = map[string]string{testCTName: "pve-other"}

	_, err = fetcher.FetchExternalResource(context.Background(), rkey)
	if err == nil || errors.Is(err, watcher.ErrExternalResourceNotFound) {
		t.Fatalf("FetchExternalResource = %v, want an error that is not a removal", err)
	}
}

func TestContainerFetcher_ClusterLookupFails_IsNotARemoval(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{})
	f.clusterErr = "cluster not ready - no quorum?"
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-no-quorum", true)

	_, err := fetcher.FetchExternalResource(context.Background(), rkey)
	if err == nil || errors.Is(err, watcher.ErrExternalResourceNotFound) {
		t.Fatalf("FetchExternalResource = %v, want an error that is not a removal", err)
	}
}

func TestContainerFetcher_ProxmoxError_IsNotARemoval(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineRunningState, pingErr: "got timeout"},
	})
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-timeout", true)

	_, err := fetcher.FetchExternalResource(context.Background(), rkey)
	if err == nil || errors.Is(err, watcher.ErrExternalResourceNotFound) {
		t.Fatalf("FetchExternalResource = %v, want an error that is not a removal", err)
	}
	if f.called("GET /api2/json/cluster/resources") {
		t.Error("a failed read was treated as a missing container")
	}
}

// The watcher poll is what keeps the cache fresh, so a reconcile reads what the
// poll saw without another API call.
func TestContainerFetcher_PollRefreshesTheCache(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineRunningState, cores: 2, memory: 512},
	})
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-refresh", true)
	pc, err := NewProxmoxClientFromRef(context.Background(), fetcher.Client, rkey.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	f.cts[100].cores = 4 // `pct set 100 --cores 4`
	f.cts[100].status = VirtualMachineStoppedState
	if ct, _ := pc.GetContainer(testCTName, lxcTestNode); ct.CPUs != 2 {
		t.Fatalf("GetContainer before the poll = %d cores, want the cached 2", ct.CPUs)
	}
	if _, err = fetcher.FetchExternalResource(context.Background(), rkey); err != nil {
		t.Fatal(err)
	}

	reads := f.countCalls("GET /api2/json/nodes/" + lxcTestNode + "/lxc/100/status/current")
	ct, err := pc.GetContainer(testCTName, lxcTestNode)
	if err != nil {
		t.Fatal(err)
	}
	if ct.CPUs != 4 || ct.Status != VirtualMachineStoppedState {
		t.Fatalf("GetContainer after the poll = %d cores, %q; the poll did not refresh the cache", ct.CPUs, ct.Status)
	}
	if got := f.countCalls("GET /api2/json/nodes/" + lxcTestNode + "/lxc/100/status/current"); got != reads {
		t.Error("GetContainer went to Proxmox although the poll had just refreshed the cache")
	}
}

func TestContainerFetcher_PollFollowsDestroyAndRecreate(t *testing.T) {
	f, _ := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineRunningState}})
	fetcher, _, rkey := containerWatcherFixture(t, f, "lxc-recreate", true)
	pc, err := NewProxmoxClientFromRef(context.Background(), fetcher.Client, rkey.ConnectionRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	delete(f.cts, 100) // `pct destroy 100`
	_, err = fetcher.FetchExternalResource(context.Background(), rkey)
	assertRemoved(t, err)
	if ok, existsErr := pc.ContainerExists(testCTName, lxcTestNode); existsErr != nil || ok {
		t.Fatalf("ContainerExists after the poll = %v, %v; want false, nil", ok, existsErr)
	}
	if pc.getCachedContainer(lxcTestNode, 100) != nil {
		t.Error("the destroyed container object is still cached")
	}

	f.cts[101] = &fakeCT{name: testCTName, status: VirtualMachineStoppedState} // created again by hand
	ct, err := pc.GetContainer(testCTName, lxcTestNode)
	if err != nil {
		t.Fatal(err)
	}
	if ct.VMID != 101 {
		t.Fatalf("GetContainer VMID = %d, want 101: the destroyed VMID is still cached", ct.VMID)
	}
}

func TestUpdateContainer_DropsTheCachedObject(t *testing.T) {
	_, pc := newFakeLXCHost(t, map[int]*fakeCT{
		100: {name: testCTName, status: VirtualMachineRunningState, cores: 2, memory: 512},
	})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	err := pc.UpdateContainer(&proxmoxv1alpha1.Container{Spec: proxmoxv1alpha1.ContainerSpec{
		Name: testCTName, NodeName: lxcTestNode,
		Template: proxmoxv1alpha1.ContainerTemplate{Cores: 4, Memory: 512},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if pc.getCachedContainer(lxcTestNode, 100) != nil {
		t.Error("the cached object still holds the old cores")
	}
}

func TestRestartContainer_DoesNotReadTheCachedStatus(t *testing.T) {
	_, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {name: testCTName, status: VirtualMachineStoppedState}})
	if _, err := pc.GetContainer(testCTName, lxcTestNode); err != nil {
		t.Fatal(err)
	}

	// The fake is running again as soon as the reboot returns. A read of the
	// cached object would keep answering stopped.
	restarted, err := pc.RestartContainer(testCTName, lxcTestNode)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("RestartContainer = false although the container is running")
	}
}

// containerWatcherFixture returns a fetcher whose Kubernetes client holds a
// Container and a ProxmoxConnection pointed at f. The connection name has to be
// unique because NewProxmoxClientFromRef caches clients by name.
func containerWatcherFixture(t *testing.T, f *fakeLXCHost, connName string, autoStart bool) (
	*ContainerFetcher, types.NamespacedName, ProxmoxResourceKey) {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	connRef := &corev1.LocalObjectReference{Name: connName}
	t.Cleanup(func() {
		clientCacheMutex.Lock()
		defer clientCacheMutex.Unlock()
		delete(clientCache, connName)
	})
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: connName, ResourceVersion: "1"},
		Spec:       proxmoxv1alpha1.ProxmoxConnectionSpec{Endpoint: f.url, TokenID: "test@pve!t", Secret: "secret"},
	}
	ct := &proxmoxv1alpha1.Container{
		ObjectMeta: metav1.ObjectMeta{Name: testCTName},
		Spec: proxmoxv1alpha1.ContainerSpec{
			Name: testCTName, NodeName: lxcTestNode, EnableAutoStart: &autoStart, ConnectionRef: connRef,
			Template: proxmoxv1alpha1.ContainerTemplate{Cores: 2, Memory: 512},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn, ct).WithStatusSubresource(ct).Build()
	return &ContainerFetcher{Client: cl}, types.NamespacedName{Name: ct.Name},
		ProxmoxResourceKey{Name: testCTName, NodeName: lxcTestNode, ConnectionRef: connRef}
}
