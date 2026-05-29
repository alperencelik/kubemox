package virtualmachine

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// PowerVerb selects which Proxmox lifecycle call the storage will trigger.
type PowerVerb int

const (
	PowerVerbStart PowerVerb = iota
	PowerVerbStop
	PowerVerbReboot
	PowerVerbShutdown
)

func (v PowerVerb) toProxmox() proxmox.PowerVerb {
	switch v {
	case PowerVerbStart:
		return proxmox.PowerStart
	case PowerVerbStop:
		return proxmox.PowerStop
	case PowerVerbReboot:
		return proxmox.PowerReboot
	case PowerVerbShutdown:
		return proxmox.PowerShutdown
	}
	return ""
}

// PowerStorage implements POST <vm>/<verb>. The request body decodes into
// a PowerAction; the response is the same envelope with TaskRef populated
// from the Proxmox UPID returned by PowerVMAsync.
type PowerStorage struct {
	resolver *proxmoxconn.Resolver
	verb     PowerVerb
}

func NewPowerStorage(r *proxmoxconn.Resolver, verb PowerVerb) *PowerStorage {
	return &PowerStorage{resolver: r, verb: verb}
}

var (
	_ rest.Storage      = (*PowerStorage)(nil)
	_ rest.NamedCreater = (*PowerStorage)(nil)
	_ rest.Scoper       = (*PowerStorage)(nil)
)

func (s *PowerStorage) New() runtime.Object   { return &pveopsv1alpha1.PowerAction{} }
func (s *PowerStorage) Destroy()              {}
func (s *PowerStorage) NamespaceScoped() bool { return false }

func (s *PowerStorage) Create(ctx context.Context, name string, obj runtime.Object,
	_ rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {

	action, ok := obj.(*pveopsv1alpha1.PowerAction)
	if !ok {
		return nil, fmt.Errorf("expected PowerAction, got %T", obj)
	}

	target, err := s.resolver.ResolveVM(ctx, name)
	if err != nil {
		return nil, err
	}

	verb := s.verb.toProxmox()
	if verb == "" {
		return nil, fmt.Errorf("unknown power verb %d", s.verb)
	}

	upid, err := target.Client.PowerVMAsync(ctx, target.VMName, target.NodeName, verb)
	if err != nil {
		return nil, err
	}

	out := action.DeepCopy()
	out.ObjectMeta = metav1.ObjectMeta{Name: name}
	// upid may be empty if the VM was already in the desired state.
	if upid != "" {
		out.TaskRef = &pveopsv1alpha1.TaskRef{UPID: upid, Node: target.NodeName}
	}
	return out, nil
}
