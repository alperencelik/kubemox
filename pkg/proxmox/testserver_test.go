package proxmox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// fakeVM is one entry as /nodes/{node}/qemu reports it.
type fakeVM struct {
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// fakeProxmox stands in for the Proxmox VE API over HTTP. The package has no
// HTTP-level tests at all - everything so far is built on a fake Kubernetes
// client - so anything about how kubemox addresses VMs on the Proxmox side is
// currently unverifiable. That is exactly the surface the VMID work changes,
// hence this.
//
// Only the three endpoints the VM lookup path touches are served; anything
// else is a 404 so an unexpected call fails loudly rather than silently
// returning a zero value.
type fakeProxmox struct {
	server *httptest.Server
	// vmsByNode is the inventory the fake cluster reports, node name -> VMs.
	vmsByNode map[string][]fakeVM
	// requests counts calls per path, so a test can assert that the cache
	// actually spared a round trip instead of assuming it did.
	requests map[string]int
}

func newFakeProxmox(t *testing.T, vmsByNode map[string][]fakeVM) *fakeProxmox {
	t.Helper()
	f := &fakeProxmox{vmsByNode: vmsByNode, requests: map[string]int{}}
	mux := http.NewServeMux()

	// GET /nodes - the node list, with every node online.
	mux.HandleFunc("/api2/json/nodes", func(w http.ResponseWriter, r *http.Request) {
		f.requests[r.URL.Path]++
		nodes := make([]map[string]any, 0, len(f.vmsByNode))
		for name := range f.vmsByNode {
			nodes = append(nodes, map[string]any{"node": name, "status": "online", "type": "node"})
		}
		writeData(w, nodes)
	})

	// GET /nodes/{node}/status and GET /nodes/{node}/qemu
	mux.HandleFunc("/api2/json/nodes/", func(w http.ResponseWriter, r *http.Request) {
		f.requests[r.URL.Path]++
		rest := strings.TrimPrefix(r.URL.Path, "/api2/json/nodes/")
		parts := strings.SplitN(rest, "/", 2)
		node := parts[0]
		if _, known := f.vmsByNode[node]; !known {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 2 && parts[1] == "status":
			writeData(w, map[string]any{"uptime": 1000})
		case len(parts) == 2 && parts[1] == "qemu":
			writeData(w, f.vmsByNode[node])
		default:
			http.NotFound(w, r)
		}
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// client returns a ProxmoxClient pointed at the fake. Token auth is used
// deliberately: it needs no login round trip, so the request counts a test
// sees are only the calls under test.
func (f *fakeProxmox) client() *ProxmoxClient {
	return NewProxmoxClient(&proxmoxv1alpha1.ProxmoxConnection{
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: f.server.URL + "/api2/json",
			TokenID:  "root@pam!test",
			Secret:   "not-a-real-secret",
		},
	})
}

// rename changes a machine's name the way an operator would with `qm set`,
// behind the controller's back.
func (f *fakeProxmox) rename(node string, vmID int, newName string) {
	for i := range f.vmsByNode[node] {
		if f.vmsByNode[node][i].VMID == vmID {
			f.vmsByNode[node][i].Name = newName
			return
		}
	}
}

func writeData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
		panic(fmt.Sprintf("fake proxmox: encoding response: %v", err))
	}
}
