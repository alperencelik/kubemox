package proxmox

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// TestContainerWatcher_Drift runs one watcher poll (desired state, fetch,
// transform, compare) against the fake Proxmox for each case.
func TestContainerWatcher_Drift(t *testing.T) {
	cases := []struct {
		name      string
		autoStart bool
		ct        *fakeCT // nil: no such container on Proxmox
		drift     bool
	}{
		{"running as asked", true, &fakeCT{status: VirtualMachineRunningState, cores: 2, memory: 512}, false},
		{"stopped with autostart", true, &fakeCT{status: VirtualMachineStoppedState, cores: 2, memory: 512}, true},
		{"stopped without autostart", false, &fakeCT{status: VirtualMachineStoppedState, cores: 2, memory: 512}, false},
		{"running without autostart", false, &fakeCT{status: VirtualMachineRunningState, cores: 2, memory: 512}, false},
		{"resized", true, &fakeCT{status: VirtualMachineRunningState, cores: 4, memory: 512}, true},
		{"destroyed", true, nil, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cts := map[int]*fakeCT{}
			if tc.ct != nil {
				tc.ct.name = testCTName
				cts[100] = tc.ct
			}
			f, _ := newFakeLXCHost(t, cts)
			fetcher, key := containerWatcherFixture(t, f, i, tc.autoStart)

			desired, err := fetcher.GetDesiredState(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := fetcher.FetchExternalResource(context.Background(), ContainerConfigExtractor(
				&proxmoxv1alpha1.Container{ObjectMeta: metav1.ObjectMeta{Name: key.Name}}).ResourceKey)
			if err != nil {
				t.Fatalf("FetchExternalResource = %v; a failed poll never triggers a reconcile", err)
			}
			actual, err := fetcher.TransformExternalState(raw)
			if err != nil {
				t.Fatal(err)
			}
			comparator := ContainerStateComparator()
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

// containerWatcherFixture returns a fetcher whose Kubernetes client holds a
// Container and a ProxmoxConnection pointed at f. The connection name is
// unique per case because NewProxmoxClientFromRef caches clients by name.
func containerWatcherFixture(t *testing.T, f *fakeLXCHost, i int, autoStart bool) (
	*ContainerFetcher, types.NamespacedName) {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	connRef := &corev1.LocalObjectReference{Name: "lxc-watch-" + string(rune('a'+i))}
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: connRef.Name, ResourceVersion: "1"},
		Spec:       proxmoxv1alpha1.ProxmoxConnectionSpec{Endpoint: f.url, TokenID: "test@pve!t", Secret: "secret"},
	}
	ct := testContainer("a", 0)
	ct.Spec.EnableAutoStart = autoStart
	ct.Spec.ConnectionRef = connRef
	ct.Spec.Template = proxmoxv1alpha1.ContainerTemplate{Cores: 2, Memory: 512}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn, ct).Build()
	return &ContainerFetcher{Client: cl}, types.NamespacedName{Name: ct.Name}
}
