package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/luthermonson/go-proxmox"
)

// fakePVE is the slice of the Proxmox API the disk operations touch, with
// the behaviour observed on PVE 9.2: a drive key written to the config
// allocates a volume named after the owner, import-from copies the source
// size, deleting a drive key turns it into unusedN, deleting unusedN frees
// the volume, a protected VM refuses to lose a drive but still lets one be
// reassigned, move_disk renames the volume after its new owner and refuses
// to cross nodes, and destroying a VM frees whatever is still attached.
type fakePVE struct {
	mu      sync.Mutex
	nextID  int
	vms     map[int]*fakeVM
	volumes map[string]int // volid -> size in GiB
	freed   []string       // volids freed, in order
	calls   []string       // "METHOD path" in order
}

type fakeVM struct {
	node   string
	config map[string]string
}

var (
	reVM     = regexp.MustCompile(`^/api2/json/nodes/([^/]+)/qemu/(\d+)(/.*)?$`)
	reQemu   = regexp.MustCompile(`^/api2/json/nodes/([^/]+)/qemu$`)
	reTask   = regexp.MustCompile(`^/api2/json/nodes/([^/]+)/tasks/([^/]+)/status$`)
	reDrive  = regexp.MustCompile(`^(scsi|virtio|sata|ide)\d+$`)
	reUnused = regexp.MustCompile(`^unused(\d+)$`)
)

// Names the fake and the tests share; constants so the linter does not ask
// for them one occurrence at a time.
const (
	fakeNode       = "pve1"
	fakeOtherNode  = "pve2"
	fakeStorage    = "main-pool"
	keyName        = "name"
	keyStatus      = "status"
	keyVMID        = "vmid"
	keyProtection  = "protection"
	stateStopped   = "stopped"
	holderDisk0    = "main-pool:vm-9998-disk-0"
	sampleVolume   = "vm-disks1:vm-1-disk-0"
	templateVolume = "main-pool:base-9001-disk-0"
)

func newFakePVE() *fakePVE {
	return &fakePVE{nextID: 100, vms: map[int]*fakeVM{}, volumes: map[string]int{}}
}

func (f *fakePVE) addVM(vmid int, node, name string, extra map[string]string) {
	cfg := map[string]string{keyName: name}
	for k, v := range extra {
		cfg[k] = v
	}
	f.vms[vmid] = &fakeVM{node: node, config: cfg}
}

// addVolume registers a volume that exists on a storage without belonging
// to any config here, such as a template's base disk or an import file.
func (f *fakePVE) addVolume(volid string, sizeGiB int) {
	f.volumes[volid] = sizeGiB
}

func (f *fakePVE) allocate(storage string, vmid, sizeGiB int) string {
	for n := 0; ; n++ {
		volid := fmt.Sprintf("%s:vm-%d-disk-%d", storage, vmid, n)
		if _, taken := f.volumes[volid]; !taken {
			f.volumes[volid] = sizeGiB
			return volid
		}
	}
}

func (f *fakePVE) driveString(volid string) string {
	return fmt.Sprintf("%s,size=%dG", volid, f.volumes[volid])
}

func (f *fakePVE) nextUnused(vm *fakeVM) string {
	for n := 0; ; n++ {
		key := fmt.Sprintf("unused%d", n)
		if _, taken := vm.config[key]; !taken {
			return key
		}
	}
}

func (f *fakePVE) upid(node, kind string, vmid int) string {
	return fmt.Sprintf("UPID:%s:0000AAAA:0000BBBB:0000CCCC:%s:%d:root@pam:", node, kind, vmid)
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"data":null,"errors":{"":%q},"message":%q}`, msg, msg)
}

// params reads a request body the way Proxmox does: JSON when the client
// says so, a form otherwise.
func params(r *http.Request) map[string]string {
	out := map[string]string{}
	body, _ := io.ReadAll(r.Body)
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		var raw map[string]any
		_ = json.Unmarshal(body, &raw)
		for k, v := range raw {
			out[k] = fmt.Sprintf("%v", v)
		}
		return out
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	_ = r.ParseForm()
	for k, v := range r.Form {
		out[k] = v[0]
	}
	return out
}

// isNodeStatus matches /nodes/<node>/status and nothing deeper.
func isNodeStatus(path string) bool {
	return strings.HasSuffix(path, "/status") && strings.HasPrefix(path, "/api2/json/nodes/") &&
		!reVM.MatchString(path) && !reTask.MatchString(path)
}

func (f *fakePVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)

	switch {
	case r.URL.Path == "/api2/json/version":
		writeJSON(w, map[string]any{"version": "9.2.20", "release": "9.2"})
	case r.URL.Path == "/api2/json/cluster/nextid":
		for {
			if _, taken := f.vms[f.nextID]; !taken {
				break
			}
			f.nextID++
		}
		writeJSON(w, strconv.Itoa(f.nextID))
	case r.URL.Path == "/api2/json/cluster/status":
		writeJSON(w, []any{})
	case isNodeStatus(r.URL.Path):
		writeJSON(w, map[string]any{})
	case reTask.MatchString(r.URL.Path):
		m := reTask.FindStringSubmatch(r.URL.Path)
		writeJSON(w, map[string]any{
			keyStatus: stateStopped, "exitstatus": "OK", "node": m[1], "upid": m[2],
			"type": "qmconfig", "user": "root@pam", "id": "0",
		})
	case reQemu.MatchString(r.URL.Path):
		node := reQemu.FindStringSubmatch(r.URL.Path)[1]
		f.handleQemu(w, r, node)
	case reVM.MatchString(r.URL.Path):
		m := reVM.FindStringSubmatch(r.URL.Path)
		vmid, _ := strconv.Atoi(m[2])
		f.handleVM(w, r, m[1], vmid, m[3])
	default:
		writeError(w, 501, "fake: unhandled "+r.Method+" "+r.URL.Path)
	}
}

func (f *fakePVE) handleQemu(w http.ResponseWriter, r *http.Request, node string) {
	switch r.Method {
	case http.MethodGet:
		list := []map[string]any{}
		ids := make([]int, 0, len(f.vms))
		for id := range f.vms {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		for _, id := range ids {
			vm := f.vms[id]
			if vm.node != node {
				continue
			}
			list = append(list, map[string]any{keyVMID: id, keyName: vm.config[keyName], keyStatus: stateStopped})
		}
		writeJSON(w, list)
	case http.MethodPost:
		p := params(r)
		vmid, _ := strconv.Atoi(p[keyVMID])
		if _, exists := f.vms[vmid]; exists {
			writeError(w, 500, fmt.Sprintf("unable to create VM %d: config file already exists", vmid))
			return
		}
		cfg := map[string]string{}
		for k, v := range p {
			if k == keyVMID {
				continue
			}
			cfg[k] = v
		}
		f.vms[vmid] = &fakeVM{node: node, config: cfg}
		writeJSON(w, f.upid(node, "qmcreate", vmid))
	default:
		writeError(w, 405, "method")
	}
}

func (f *fakePVE) handleVM(w http.ResponseWriter, r *http.Request, node string, vmid int, sub string) {
	vm, ok := f.vms[vmid]
	if !ok || vm.node != node {
		writeError(w, 500, fmt.Sprintf("Configuration file 'nodes/%s/qemu-server/%d.conf' does not exist", node, vmid))
		return
	}
	switch {
	case sub == "/status/current" && r.Method == http.MethodGet:
		writeJSON(w, map[string]any{keyVMID: vmid, keyName: vm.config[keyName], keyStatus: stateStopped})
	case sub == "/config" && r.Method == http.MethodGet:
		cfg := map[string]any{}
		for k, v := range vm.config {
			if k == keyProtection {
				n, _ := strconv.Atoi(v)
				cfg[k] = n
				continue
			}
			cfg[k] = v
		}
		writeJSON(w, cfg)
	case sub == "/config" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		f.applyConfig(w, vm, node, vmid, params(r))
	case sub == "/resize" && r.Method == http.MethodPut:
		p := params(r)
		value, ok := vm.config[p["disk"]]
		if !ok {
			writeError(w, 500, fmt.Sprintf("disk '%s' does not exist", p["disk"]))
			return
		}
		volid, _ := parseDriveValue(value)
		size, _ := strconv.Atoi(strings.TrimSuffix(p["size"], "G"))
		if size < f.volumes[volid] {
			writeError(w, 500, "shrinking disks is not supported")
			return
		}
		f.volumes[volid] = size
		vm.config[p["disk"]] = f.driveString(volid)
		writeJSON(w, f.upid(node, "resize", vmid))
	case sub == "/move_disk" && r.Method == http.MethodPost:
		f.moveDisk(w, vm, node, vmid, params(r))
	case sub == "" && r.Method == http.MethodDelete:
		for key, value := range vm.config {
			if reDrive.MatchString(key) || reUnused.MatchString(key) {
				volid, _ := parseDriveValue(value)
				delete(f.volumes, volid)
				f.freed = append(f.freed, volid)
			}
		}
		delete(f.vms, vmid)
		writeJSON(w, f.upid(node, "qmdestroy", vmid))
	default:
		writeError(w, 501, "fake: unhandled "+r.Method+" "+r.URL.Path)
	}
}

func (f *fakePVE) applyConfig(w http.ResponseWriter, vm *fakeVM, node string, vmid int, p map[string]string) {
	async := false
	for key, value := range p {
		switch {
		case key == "delete":
			for _, k := range strings.Split(value, ",") {
				if reDrive.MatchString(k) {
					if vm.config[keyProtection] == "1" {
						writeError(w, 500, fmt.Sprintf("can't remove drive '%s' - protection mode enabled", k))
						return
					}
					if cur, ok := vm.config[k]; ok {
						vm.config[f.nextUnused(vm)] = strings.SplitN(cur, ",", 2)[0]
					}
				}
				if m := reUnused.FindStringSubmatch(k); m != nil {
					if vm.config[keyProtection] == "1" {
						writeError(w, 500, fmt.Sprintf("can't remove drive '%s' - protection mode enabled", k))
						return
					}
					if cur, ok := vm.config[k]; ok {
						volid, _ := parseDriveValue(cur)
						delete(f.volumes, volid)
						f.freed = append(f.freed, volid)
					}
				}
				delete(vm.config, k)
			}
		case reDrive.MatchString(key):
			async = true
			// "storage:size" allocates, "storage:0,import-from=volid" copies.
			spec := strings.SplitN(value, ",", 2)
			sv := strings.SplitN(spec[0], ":", 2)
			if len(sv) != 2 {
				writeError(w, 400, "Parameter verification failed. "+key)
				return
			}
			storage := sv[0]
			size, _ := strconv.Atoi(sv[1])
			if len(spec) == 2 && strings.HasPrefix(spec[1], "import-from=") {
				src := strings.TrimPrefix(spec[1], "import-from=")
				srcSize, ok := f.volumes[src]
				if !ok {
					writeError(w, 500, fmt.Sprintf("volume '%s' does not exist", src))
					return
				}
				size = srcSize
			}
			if size <= 0 {
				writeError(w, 400, "Parameter verification failed. "+key)
				return
			}
			volid := f.allocate(storage, vmid, size)
			vm.config[key] = f.driveString(volid)
		default:
			vm.config[key] = value
		}
	}
	if async {
		writeJSON(w, f.upid(node, "qmconfig", vmid))
		return
	}
	// Changes without a disk allocation are synchronous: Proxmox answers null.
	writeJSON(w, nil)
}

func (f *fakePVE) moveDisk(w http.ResponseWriter, vm *fakeVM, node string, vmid int, p map[string]string) {
	target, _ := strconv.Atoi(p["target-vmid"])
	tvm, ok := f.vms[target]
	if !ok {
		writeError(w, 500, fmt.Sprintf("VM %d does not exist", target))
		return
	}
	if tvm.node != vm.node {
		writeError(w, 500, fmt.Sprintf("Both VMs need to be on the same node (%s != %s)", vm.node, tvm.node))
		return
	}
	value, ok := vm.config[p["disk"]]
	if !ok {
		writeError(w, 500, fmt.Sprintf("Disk '%s' for VM '%d' does not exist", p["disk"], vmid))
		return
	}
	targetDisk := p["target-disk"]
	if targetDisk == "" {
		targetDisk = p["disk"]
	}
	if _, taken := tvm.config[targetDisk]; taken {
		writeError(w, 400, "Parameter verification failed.")
		return
	}
	volid, _ := parseDriveValue(value)
	size := f.volumes[volid]
	delete(f.volumes, volid)
	delete(vm.config, p["disk"])
	storage := strings.SplitN(volid, ":", 2)[0]
	newVolid := f.allocate(storage, target, size)
	tvm.config[targetDisk] = f.driveString(newVolid)
	writeJSON(w, f.upid(node, "qmmove", vmid))
}

func newTestClient(t *testing.T, f *fakePVE) *ProxmoxClient {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	client := proxmox.NewClient(srv.URL+"/api2/json",
		proxmox.WithAPIToken("test@pve!t", "secret"),
		proxmox.WithHTTPClient(srv.Client()))
	return &ProxmoxClient{Client: client, nodesCache: make(map[string]NodeCache)}
}

func TestHolderName(t *testing.T) {
	if got := HolderName("tenant-a", fakeNode); got != "kubemox-disks-tenant-a-pve1" {
		t.Fatalf("HolderName() = %q", got)
	}
	long := HolderName(strings.Repeat("n", 50), strings.Repeat("x", 30))
	if len(long) != proxmoxNameMaxLen {
		t.Fatalf("long name is %d chars, want %d: %q", len(long), proxmoxNameMaxLen, long)
	}
	if long == HolderName(strings.Repeat("n", 50), strings.Repeat("x", 31)) {
		t.Fatal("two long names collided after truncation")
	}
}

func TestParseDriveValue(t *testing.T) {
	cases := []struct {
		in    string
		volid string
		size  int
	}{
		{holderDisk0 + ",size=10G", holderDisk0, 10},
		{holderDisk0 + ",discard=on,iothread=1,size=8G,ssd=1", holderDisk0, 8},
		{"local:iso/x.iso,media=cdrom,size=366K", "local:iso/x.iso", 1},
		{sampleVolume + ",size=512M", sampleVolume, 1},
		{sampleVolume + ",size=2T", sampleVolume, 2048},
		{sampleVolume + ",size=1048576", sampleVolume, 1},
		{sampleVolume, sampleVolume, 0},
	}
	for _, tc := range cases {
		volid, size := parseDriveValue(tc.in)
		if volid != tc.volid || size != tc.size {
			t.Errorf("parseDriveValue(%q) = (%q, %d), want (%q, %d)", tc.in, volid, size, tc.volid, tc.size)
		}
	}
}

func TestEnsureDiskHolderCreatesOnceAndFinds(t *testing.T) {
	f := newFakePVE()
	pc := newTestClient(t, f)
	ctx := context.Background()

	h, err := pc.EnsureDiskHolder(ctx, "tenant-a", fakeNode)
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "kubemox-disks-tenant-a-pve1" || h.VMID != 100 || h.Node != fakeNode {
		t.Fatalf("holder = %+v", h)
	}
	cfg := f.vms[100].config
	if cfg[keyProtection] != "1" || !strings.Contains(cfg["tags"], DiskHolderTag) || cfg["net0"] != "" {
		t.Fatalf("holder config = %v", cfg)
	}

	again, err := pc.EnsureDiskHolder(ctx, "tenant-a", fakeNode)
	if err != nil {
		t.Fatal(err)
	}
	if again.VMID != 100 || len(f.vms) != 1 {
		t.Fatalf("second call created a VM: %+v, %d VMs", again, len(f.vms))
	}

	other, err := pc.EnsureDiskHolder(ctx, "tenant-b", fakeNode)
	if err != nil {
		t.Fatal(err)
	}
	if other.VMID == h.VMID {
		t.Fatal("two namespaces share one holder")
	}
	found, err := pc.FindDiskHolder(ctx, "tenant-c", fakeNode)
	if err != nil || found != nil {
		t.Fatalf("FindDiskHolder for a namespace without one = %+v, %v", found, err)
	}
}

func TestCreateHolderDiskBlankAndImport(t *testing.T) {
	f := newFakePVE()
	f.addVM(9998, fakeNode, HolderName("tenant-a", fakeNode), map[string]string{keyProtection: "1"})
	f.addVolume(templateVolume, 8)
	pc := newTestClient(t, f)
	ctx := context.Background()
	h := &DiskHolder{Name: HolderName("tenant-a", fakeNode), VMID: 9998, Node: fakeNode}

	slot, volid, size, err := pc.CreateHolderDisk(ctx, h, fakeStorage, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if slot != "scsi0" || volid != holderDisk0 || size != 5 {
		t.Fatalf("blank disk = %s %s %d", slot, volid, size)
	}

	// An import copies the 8G source and is then grown to the 20G asked for;
	// it lands in the next free slot even though protection is on.
	slot, volid, size, err = pc.CreateHolderDisk(ctx, h, fakeStorage, 20, templateVolume)
	if err != nil {
		t.Fatal(err)
	}
	if slot != "scsi1" || volid != "main-pool:vm-9998-disk-1" || size != 20 || f.volumes[volid] != 20 {
		t.Fatalf("imported disk = %s %s %d (fake size %d)", slot, volid, size, f.volumes[volid])
	}
	if f.vms[9998].config[keyProtection] != "1" {
		t.Fatal("protection was dropped while creating a disk")
	}

	// A source smaller than the request is grown; one at least as large is
	// left alone, never shrunk.
	slot, _, size, err = pc.CreateHolderDisk(ctx, h, fakeStorage, 4, templateVolume)
	if err != nil {
		t.Fatal(err)
	}
	if slot != "scsi2" || size != 8 {
		t.Fatalf("imported disk smaller than its source = %s %d", slot, size)
	}

	if _, _, _, err = pc.CreateHolderDisk(ctx, h, fakeStorage, 1, "main-pool:does-not-exist"); err == nil {
		t.Fatal("import from a missing volume succeeded")
	}
}

func TestCreateHolderDiskRunsOutOfSlots(t *testing.T) {
	f := newFakePVE()
	extra := map[string]string{}
	for _, slot := range diskHolderSlots {
		extra[slot] = "main-pool:vm-9998-disk-x,size=1G"
	}
	f.addVM(9998, fakeNode, "h", extra)
	pc := newTestClient(t, f)
	h := &DiskHolder{Name: "h", VMID: 9998, Node: fakeNode}
	_, _, _, err := pc.CreateHolderDisk(context.Background(), h, fakeStorage, 1, "")
	if err == nil || !strings.Contains(err.Error(), "no free drive slot") {
		t.Fatalf("err = %v", err)
	}
}

func TestMoveDiskRenamesVolumeAndSurvivesDestroy(t *testing.T) {
	f := newFakePVE()
	f.addVM(9998, fakeNode, "holder", map[string]string{keyProtection: "1"})
	f.addVM(9999, fakeNode, "vm", nil)
	f.addVM(9997, fakeOtherNode, "elsewhere", nil)
	pc := newTestClient(t, f)
	ctx := context.Background()
	h := &DiskHolder{Name: "holder", VMID: 9998, Node: fakeNode}

	slot, volid, _, err := pc.CreateHolderDisk(ctx, h, fakeStorage, 10, "")
	if err != nil {
		t.Fatal(err)
	}

	// Reassign from the protected holder to the VM: allowed, and the volume
	// takes the new owner's name.
	moved, err := pc.MoveDisk(ctx, fakeNode, 9998, slot, 9999, "scsi0")
	if err != nil {
		t.Fatal(err)
	}
	if moved == volid || !strings.HasPrefix(moved, "main-pool:vm-9999-disk-") {
		t.Fatalf("moved volid = %q (was %q)", moved, volid)
	}
	if _, still := f.vms[9998].config[slot]; still {
		t.Fatal("holder still has the slot after the move")
	}

	// Across nodes Proxmox refuses before doing anything. The client hides
	// the message behind the 500 status, so only the refusal is asserted,
	// plus that the drive stayed where it was.
	if _, err = pc.MoveDisk(ctx, fakeNode, 9999, "scsi0", 9997, "scsi0"); err == nil {
		t.Fatal("cross-node move succeeded")
	}
	if _, still := f.vms[9999].config["scsi0"]; !still {
		t.Fatal("cross-node move removed the drive from its VM")
	}

	// Back to the holder, then destroy the VM: the returned volume survives.
	back, err := pc.MoveDisk(ctx, fakeNode, 9999, "scsi0", 9998, slot)
	if err != nil {
		t.Fatal(err)
	}
	vm, err := pc.diskVM(ctx, fakeNode, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = vm.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.volumes[back]; !ok {
		t.Fatalf("volume %s did not survive the VM destroy", back)
	}
	got, size, err := pc.ReadDisk(ctx, fakeNode, 9998, slot)
	if err != nil || got != back || size != 10 {
		t.Fatalf("ReadDisk after return = %q %d %v", got, size, err)
	}
}

func TestDeleteHolderDiskTogglesProtectionAndFrees(t *testing.T) {
	f := newFakePVE()
	f.addVM(9998, fakeNode, "holder", map[string]string{keyProtection: "1"})
	pc := newTestClient(t, f)
	ctx := context.Background()
	h := &DiskHolder{Name: "holder", VMID: 9998, Node: fakeNode}

	slot, volid, _, err := pc.CreateHolderDisk(ctx, h, fakeStorage, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = pc.DeleteHolderDisk(ctx, h, slot); err != nil {
		t.Fatal(err)
	}
	if _, exists := f.volumes[volid]; exists {
		t.Fatalf("volume %s still exists after delete", volid)
	}
	if len(f.freed) != 1 || f.freed[0] != volid {
		t.Fatalf("freed = %v", f.freed)
	}
	cfg := f.vms[9998].config
	if cfg[keyProtection] != "1" {
		t.Fatal("protection not restored after delete")
	}
	for k := range cfg {
		if reUnused.MatchString(k) || k == slot {
			t.Fatalf("holder config still has %s", k)
		}
	}
	// Deleting an empty slot is a no-op, and protection stays untouched.
	before := len(f.calls)
	if err = pc.DeleteHolderDisk(ctx, h, slot); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls[before:] {
		if strings.HasPrefix(c, "POST") || strings.HasPrefix(c, "PUT") {
			t.Fatalf("deleting an empty slot wrote to Proxmox: %s", c)
		}
	}
}

func TestWaitDiskTaskNilIsSynchronous(t *testing.T) {
	if err := waitDiskTask(context.Background(), nil, diskConfigTimeout); err != nil {
		t.Fatal(err)
	}
}
