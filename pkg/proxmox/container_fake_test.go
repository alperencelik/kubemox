package proxmox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/luthermonson/go-proxmox"
)

// lxcTestNode is the one node the fake LXC host answers for.
const lxcTestNode = "pve-ct"

// fakeCT is one container on the fake LXC host.
type fakeCT struct {
	name   string
	status string
	// lockErr, when set, makes destroy fail with this message, the way a
	// locked or protected container refuses it.
	lockErr string
}

// fakeLXCHost is the slice of the Proxmox API the Container code touches.
// Errors come back the way Proxmox sends them: the message rides in the HTTP
// status line, which is where go-proxmox reads it from.
type fakeLXCHost struct {
	mu    sync.Mutex
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
	client := proxmox.NewClient(srv.URL+"/api2/json",
		proxmox.WithAPIToken("test@pve!t", "secret"),
		proxmox.WithHTTPClient(srv.Client()))
	return f, &ProxmoxClient{Client: client, nodesCache: make(map[string]NodeCache)}
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

func (f *fakeLXCHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, "/api2/json/nodes/"+lxcTestNode)
	switch {
	case path == "/status":
		writeCTData(w, map[string]any{"uptime": 1})
	case path == "/lxc":
		list := make([]map[string]any, 0, len(f.cts))
		for id, ct := range f.cts {
			list = append(list, ct.summary(id))
		}
		writeCTData(w, list)
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
		writeCTData(w, map[string]any{"hostname": ct.name})
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

// summary is the container as /lxc and /lxc/{vmid}/status/current list it.
func (ct *fakeCT) summary(id int) map[string]any {
	return map[string]any{"vmid": id, "name": ct.name, "status": ct.status}
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
