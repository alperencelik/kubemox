// Package node serves /apis/ops.proxmox.alperen.cloud/v1alpha1/nodes —
// a read-only projection of every node across every known ProxmoxConnection.
package node

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
)

// Storage is the list/get storage for Node.
type Storage struct {
	resolver *proxmoxconn.Resolver
}

func NewStorage(r *proxmoxconn.Resolver) *Storage {
	return &Storage{resolver: r}
}

var (
	_ rest.Storage              = (*Storage)(nil)
	_ rest.Lister               = (*Storage)(nil)
	_ rest.Getter               = (*Storage)(nil)
	_ rest.Scoper               = (*Storage)(nil)
	_ rest.SingularNameProvider = (*Storage)(nil)
)

func (s *Storage) New() runtime.Object     { return &pveopsv1alpha1.Node{} }
func (s *Storage) NewList() runtime.Object { return &pveopsv1alpha1.NodeList{} }
func (s *Storage) Destroy()                {}
func (s *Storage) NamespaceScoped() bool   { return false }
func (s *Storage) GetSingularName() string { return "node" }

// Get returns a Node by name. Name is the Proxmox node name (unique
// within a connection). If two connections expose nodes with the same
// name, the first match wins; in practice a kubemox install talks to one
// Proxmox cluster.
func (s *Storage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	list, err := s.listAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Name == name {
			return &list.Items[i], nil
		}
	}
	return nil, fmt.Errorf("node %q not found", name)
}

func (s *Storage) List(ctx context.Context, _ *internalversion.ListOptions) (runtime.Object, error) {
	return s.listAll(ctx)
}

// ConvertToTable renders the kubectl-friendly columns.
func (s *Storage) ConvertToTable(ctx context.Context, obj runtime.Object,
	_ runtime.Object) (*metav1.Table, error) {
	t := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Status", Type: "string"},
			{Name: "CPU%", Type: "number"},
			{Name: "Mem Used", Type: "integer"},
			{Name: "Connection", Type: "string"},
		},
	}
	add := func(n *pveopsv1alpha1.Node) {
		t.Rows = append(t.Rows, metav1.TableRow{
			Cells:  []interface{}{n.Name, n.Status, n.CPUUsage, n.MemoryUsedBytes, n.ConnectionName},
			Object: runtime.RawExtension{Object: n},
		})
	}
	switch v := obj.(type) {
	case *pveopsv1alpha1.Node:
		add(v)
	case *pveopsv1alpha1.NodeList:
		for i := range v.Items {
			add(&v.Items[i])
		}
	}
	return t, nil
}

func (s *Storage) listAll(ctx context.Context) (*pveopsv1alpha1.NodeList, error) {
	conns, err := s.resolver.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := &pveopsv1alpha1.NodeList{}

	// TODO: this hits Proxmox once per connection on every list call. For
	// large clusters, fold into a watch-driven cache in pkg/proxmox.
	for i := range conns {
		conn := &conns[i]
		pc, err := s.resolver.ClientFor(ctx, conn.Name)
		if err != nil {
			continue // skip dead connections — surface in conditions later
		}
		nodes, err := pc.Client.Nodes(ctx)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			out.Items = append(out.Items, pveopsv1alpha1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: n.Node,
				},
				ConnectionName:   conn.Name,
				Status:           n.Status,
				CPUUsage:         n.CPU,
				CPUCount:         int(n.MaxCPU),
				MemoryUsedBytes:  int64(n.Mem),
				MemoryTotalBytes: int64(n.MaxMem),
				UptimeSeconds:    int64(n.Uptime),
			})
		}
	}
	return out, nil
}
