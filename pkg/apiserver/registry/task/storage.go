// Package task serves /apis/ops.proxmox.alperen.cloud/v1alpha1/tasks —
// clients poll this resource with a Proxmox UPID to check long-running
// operations (start, stop, etc.).
package task

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
)

// Storage implements GET on /tasks/{upid}. The UPID embeds the node it ran
// on; to find the right ProxmoxConnection we iterate connections and try
// each until one succeeds. With the usual single-connection deployment
// this is O(1).
type Storage struct {
	resolver *proxmoxconn.Resolver
}

func NewStorage(r *proxmoxconn.Resolver) *Storage {
	return &Storage{resolver: r}
}

var (
	_ rest.Storage              = (*Storage)(nil)
	_ rest.Getter               = (*Storage)(nil)
	_ rest.Scoper               = (*Storage)(nil)
	_ rest.SingularNameProvider = (*Storage)(nil)
	_ rest.TableConvertor       = (*Storage)(nil)
)

func (s *Storage) New() runtime.Object     { return &pveopsv1alpha1.Task{} }
func (s *Storage) Destroy()                {}
func (s *Storage) NamespaceScoped() bool   { return false }
func (s *Storage) GetSingularName() string { return "task" }

func (s *Storage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	if name == "" || !strings.HasPrefix(name, "UPID:") {
		return nil, fmt.Errorf("name must be a Proxmox UPID (got %q)", name)
	}

	conns, err := s.resolver.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for i := range conns {
		conn := &conns[i]
		pc, err := s.resolver.ClientFor(ctx, conn.Name)
		if err != nil {
			lastErr = err
			continue
		}
		status, err := pc.GetTaskStatus(ctx, name)
		if err != nil {
			lastErr = err
			continue
		}
		out := &pveopsv1alpha1.Task{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Node:       status.Node,
			Type:       status.Type,
			User:       status.User,
			Status:     status.Status,
			ExitStatus: status.ExitStatus,
		}
		if !status.StartTime.IsZero() {
			out.StartTime = metav1.NewTime(status.StartTime)
		}
		if !status.EndTime.IsZero() {
			t := metav1.NewTime(status.EndTime)
			out.EndTime = &t
		}
		return out, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("task %q not found in any ProxmoxConnection: %w", name, lastErr)
	}
	return nil, fmt.Errorf("task %q not found", name)
}

// ConvertToTable provides the default columns shown by kubectl. The UPID
// can be long; printing Node + Type + Status up front lets a `kubectl get`
// be useful at a glance without `-o wide`.
func (s *Storage) ConvertToTable(_ context.Context, obj runtime.Object,
	_ runtime.Object) (*metav1.Table, error) {
	t := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Node", Type: "string"},
			{Name: "Type", Type: "string"},
			{Name: "Status", Type: "string"},
			{Name: "Exit", Type: "string"},
		},
	}
	add := func(task *pveopsv1alpha1.Task) {
		t.Rows = append(t.Rows, metav1.TableRow{
			Cells:  []interface{}{task.Name, task.Node, task.Type, task.Status, task.ExitStatus},
			Object: runtime.RawExtension{Object: task},
		})
	}
	switch v := obj.(type) {
	case *pveopsv1alpha1.Task:
		add(v)
	case *pveopsv1alpha1.TaskList:
		for i := range v.Items {
			add(&v.Items[i])
		}
	}
	return t, nil
}
