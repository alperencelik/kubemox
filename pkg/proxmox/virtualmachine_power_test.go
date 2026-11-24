package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const testVMName = "vm-power"

var fakeVMPath = regexp.MustCompile(`^/qemu/(\d+)(/.*)?$`)

// fakeVM is one QEMU machine on the fake host.
type fakeVM struct {
	status string
}

// fakeVMSummary is a machine as /qemu and /qemu/{vmid}/status/current list it.
type fakeVMSummary struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (f *fakeLXCHost) serveVMList(w http.ResponseWriter) {
	list := make([]fakeVMSummary, 0, len(f.vms))
	for id, vm := range f.vms {
		list = append(list, fakeVMSummary{id, testVMName, vm.status})
	}
	writeCTData(w, list)
}

func (f *fakeLXCHost) serveVM(w http.ResponseWriter, r *http.Request, id int, sub string) {
	vm := f.vms[id]
	switch r.Method + " " + sub {
	case "GET /status/current":
		writeCTData(w, fakeVMSummary{id, testVMName, vm.status})
	case "GET /config":
		writeCTData(w, map[string]any{
			"cores": 2, "sockets": 1, "memory": 512,
			"net0":  "virtio=BC:24:11:00:00:01,bridge=vmbr0",
			"scsi0": "local-lvm:vm-100-disk-0,size=10G",
		})
	case "POST /config":
		writeCTData(w, fakeCTUPID("qmconfig", id))
	case "POST /status/stop":
		vm.status = VirtualMachineStoppedState
		writeCTData(w, fakeCTUPID("qmstop", id))
	case "POST /status/start":
		vm.status = VirtualMachineRunningState
		writeCTData(w, fakeCTUPID("qmstart", id))
	default:
		http.NotFound(w, r)
	}
}

func testVM(autoStart *bool) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: testVMName},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name: testVMName, NodeName: lxcTestNode, EnableAutoStart: autoStart,
			VMSpec: &proxmoxv1alpha1.NewVMSpec{
				Cores: 2, Socket: 1, Memory: 512,
				Disk:    []proxmoxv1alpha1.VirtualMachineDisk{{Device: "scsi0", Storage: "local-lvm", Size: 10}},
				Network: &[]proxmoxv1alpha1.VirtualMachineNetwork{{Model: "virtio", Bridge: "vmbr0"}},
			},
		},
	}
}

// vmWatcherFixture is containerWatcherFixture for a VirtualMachine.
func vmWatcherFixture(t *testing.T, f *fakeLXCHost, connName string, autoStart *bool) (
	*VirtualMachineFetcher, types.NamespacedName, ProxmoxResourceKey) {
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
	vm := testVM(autoStart)
	vm.Spec.ConnectionRef = connRef
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn, vm).WithStatusSubresource(vm).Build()
	return &VirtualMachineFetcher{Client: cl}, types.NamespacedName{Name: vm.Name},
		ProxmoxResourceKey{Name: testVMName, NodeName: lxcTestNode, ConnectionRef: connRef}
}

func TestVirtualMachineFetcher_PowerDrift(t *testing.T) {
	cases := []struct {
		name      string
		autoStart *bool
		status    string
		drift     bool
	}{
		{"running as asked", ptr.To(true), VirtualMachineRunningState, false},
		{"stopped with autostart", ptr.To(true), VirtualMachineStoppedState, true},
		{"stopped with autostart unset", nil, VirtualMachineStoppedState, true},
		{"stopped without autostart", ptr.To(false), VirtualMachineStoppedState, false},
		{"running without autostart", ptr.To(false), VirtualMachineRunningState, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newFakeLXCHost(t, nil)
			f.vms = map[int]*fakeVM{100: {status: tc.status}}
			fetcher, key, rkey := vmWatcherFixture(t, f, fmt.Sprintf("vm-drift-%d", i), tc.autoStart)

			desired, err := fetcher.GetDesiredState(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := fetcher.FetchExternalResource(context.Background(), rkey)
			if err != nil {
				t.Fatalf("FetchExternalResource = %v", err)
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

// A PCI change on a stopped machine must not start it.
func TestConfigureVirtualMachinePCI_KeepsPowerState(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		restarted bool
	}{
		{"running machine is restarted", VirtualMachineRunningState, true},
		{"stopped machine stays stopped", VirtualMachineStoppedState, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, pc := newFakeLXCHost(t, nil)
			f.vms = map[int]*fakeVM{100: {status: tc.status}}
			vm := testVM(ptr.To(false))
			vm.Spec.VMSpec.PciDevices = []proxmoxv1alpha1.PciDevice{{Type: "raw", DeviceID: "0000:01:00.0"}}

			if err := pc.configureVirtualMachinePCI(vm); err != nil {
				t.Fatal(err)
			}
			path := "/api2/json/nodes/" + lxcTestNode + "/qemu/100/status/"
			if got := f.called("POST " + path + "stop"); got != tc.restarted {
				t.Errorf("stop called = %v, want %v", got, tc.restarted)
			}
			if got := f.called("POST " + path + "start"); got != tc.restarted {
				t.Errorf("start called = %v, want %v", got, tc.restarted)
			}
			if f.vms[100].status != tc.status {
				t.Errorf("status = %q, want %q", f.vms[100].status, tc.status)
			}
		})
	}
}
