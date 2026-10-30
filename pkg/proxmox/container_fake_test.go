package proxmox

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/luthermonson/go-proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// lxcTestNode is the one node the fake LXC host answers for.
const lxcTestNode = "pve-ct"

const testCTName = "web"

// fakeCT is one container on the fake LXC host.
type fakeCT struct {
	name   string
	status string
	cores  int
	memory int // MiB
	tags   string
	// lockErr, when set, makes destroy fail with this message, the way a
	// locked or protected container refuses it.
	lockErr string
	// config holds every other key written by create, clone or a config PUT.
	config map[string]string
}

// fakeLXCHost is the slice of the Proxmox API the Container code touches.
// Errors come back the way Proxmox sends them: the message rides in the HTTP
// status line, which is where go-proxmox reads it from.
type fakeLXCHost struct {
	mu    sync.Mutex
	url   string // API base URL, for a ProxmoxConnection endpoint
	cts   map[int]*fakeCT
	calls []string // "METHOD path" in order
}

var fakeCTPath = regexp.MustCompile(`^/lxc/(\d+)(/.*)?$`)

// newFakeLXCHost starts the fake and returns a ProxmoxClient pointed at it.
func newFakeLXCHost(t *testing.T, cts map[int]*fakeCT) (*fakeLXCHost, *ProxmoxClient) {
	t.Helper()
	f := &fakeLXCHost{cts: cts}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL + "/api2/json"
	client := proxmox.NewClient(f.url,
		proxmox.WithAPIToken("test@pve!t", "secret"),
		proxmox.WithHTTPClient(srv.Client()))
	return f, &ProxmoxClient{Client: client, nodesCache: make(map[string]NodeCache)}
}

// testContainer is a Container object for spec.name testCTName on the fake
// node, with the given UID and recorded VMID (0: none yet).
func testContainer(uid string, vmid int) *proxmoxv1alpha1.Container {
	ct := &proxmoxv1alpha1.Container{
		ObjectMeta: metav1.ObjectMeta{Name: "ct-" + uid, UID: types.UID(uid)},
		Spec:       proxmoxv1alpha1.ContainerSpec{Name: testCTName, NodeName: lxcTestNode},
	}
	ct.Status.Status.ID = vmid
	return ct
}

func (f *fakeLXCHost) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (f *fakeLXCHost) nextID() int {
	id := 100
	for f.cts[id] != nil {
		id++
	}
	return id
}

func (f *fakeLXCHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	switch r.URL.Path {
	case "/api2/json/cluster/status":
		writeCTData(w, []any{})
		return
	case "/api2/json/cluster/nextid":
		writeCTData(w, strconv.Itoa(f.nextID()))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api2/json/nodes/"+lxcTestNode)
	switch {
	case path == "/status":
		writeCTData(w, map[string]any{"uptime": 1})
	case path == "/lxc" && r.Method == http.MethodGet:
		list := make([]map[string]any, 0, len(f.cts))
		for id, ct := range f.cts {
			list = append(list, ct.summary(id))
		}
		writeCTData(w, list)
	case path == "/lxc" && r.Method == http.MethodPost:
		p := ctParams(r)
		id, _ := strconv.Atoi(p["vmid"])
		if f.cts[id] != nil {
			writePVEError(w, fmt.Sprintf("CT %d already exists", id))
			return
		}
		ct := &fakeCT{status: VirtualMachineStoppedState, config: map[string]string{}}
		ct.set(p)
		f.cts[id] = ct
		writeCTData(w, fakeCTUPID("vzcreate", id))
	case strings.HasPrefix(path, "/tasks/"):
		writeCTData(w, map[string]any{"status": "stopped", "exitstatus": "OK"})
	case fakeCTPath.MatchString(path):
		m := fakeCTPath.FindStringSubmatch(path)
		id, _ := strconv.Atoi(m[1])
		f.serveCT(w, r, id, m[2])
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeLXCHost) serveCT(w http.ResponseWriter, r *http.Request, id int, sub string) {
	ct, ok := f.cts[id]
	if !ok {
		writePVEError(w, fmt.Sprintf("Configuration file 'nodes/%s/lxc/%d.conf' does not exist", lxcTestNode, id))
		return
	}
	switch r.Method + " " + sub {
	case "GET /status/current":
		writeCTData(w, ct.summary(id))
	case "GET /config":
		cfg := map[string]any{
			"hostname": ct.name, virtualMachineCPUOption: ct.cores, virtualMachineMemoryOption: ct.memory,
			containerOptionTags: ct.tags,
		}
		for k, v := range ct.config {
			cfg[k] = v
		}
		writeCTData(w, cfg)
	case "PUT /config":
		ct.set(ctParams(r))
		writeCTData(w, nil)
	case "PUT /resize":
		p := ctParams(r)
		volume, _, _ := strings.Cut(ct.config[p["disk"]], ",")
		ct.config[p["disk"]] = volume + ",size=" + p["size"]
		writeCTData(w, fakeCTUPID("resize", id))
	case "POST /clone":
		p := ctParams(r)
		newID, _ := strconv.Atoi(p["newid"])
		clone := &fakeCT{status: VirtualMachineStoppedState, cores: ct.cores, memory: ct.memory,
			tags: ct.tags, config: map[string]string{}}
		for k, v := range ct.config {
			clone.config[k] = v
		}
		clone.name = p["hostname"]
		f.cts[newID] = clone
		writeCTData(w, fakeCTUPID("vzclone", id))
	case "POST /status/stop":
		ct.status = VirtualMachineStoppedState
		writeCTData(w, fakeCTUPID("vzstop", id))
	case "POST /status/start":
		ct.status = VirtualMachineRunningState
		writeCTData(w, fakeCTUPID("vzstart", id))
	case "DELETE ":
		switch {
		case ct.lockErr != "":
			writePVEError(w, ct.lockErr)
		case ct.status == VirtualMachineRunningState:
			writePVEError(w, fmt.Sprintf("CT %d is running - destroy failed", id))
		default:
			delete(f.cts, id)
			writeCTData(w, fakeCTUPID("vzdestroy", id))
		}
	default:
		http.NotFound(w, r)
	}
}

// set applies create, clone or config parameters to the container.
func (ct *fakeCT) set(p map[string]string) {
	if ct.config == nil {
		ct.config = map[string]string{}
	}
	for k, v := range p {
		switch k {
		case "vmid":
		case "hostname":
			ct.name = v
		case containerOptionTags:
			ct.tags = v
		case virtualMachineCPUOption:
			ct.cores, _ = strconv.Atoi(v)
		case virtualMachineMemoryOption:
			ct.memory, _ = strconv.Atoi(v)
		default:
			ct.config[k] = v
		}
	}
}

// summary is the container as /lxc and /lxc/{vmid}/status/current list it.
func (ct *fakeCT) summary(id int) map[string]any {
	return map[string]any{
		"vmid": id, "name": ct.name, "status": ct.status, containerOptionTags: ct.tags,
		"cpus": ct.cores, "maxmem": ct.memory * 1024 * 1024,
	}
}

// ctParams reads a JSON request body, which is what go-proxmox sends.
func ctParams(r *http.Request) map[string]string {
	out := map[string]string{}
	body, _ := io.ReadAll(r.Body)
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	for k, v := range raw {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

func fakeCTUPID(kind string, id int) string {
	return fmt.Sprintf("UPID:%s:00000001:00000001:00000001:%s:%d:root@pam:", lxcTestNode, kind, id)
}

func writeCTData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// writePVEError answers 500 with msg as the reason phrase, as pveproxy does.
// net/http only writes the standard phrase, so the response is written raw.
func writePVEError(w http.ResponseWriter, msg string) {
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	defer func() { _ = conn.Close() }()
	body := `{"data":null}`
	_, _ = fmt.Fprintf(buf, "HTTP/1.1 500 %s\r\nContent-Type: application/json\r\n"+
		"Content-Length: %d\r\nConnection: close\r\n\r\n%s", msg, len(body), body)
	_ = buf.Flush()
}
