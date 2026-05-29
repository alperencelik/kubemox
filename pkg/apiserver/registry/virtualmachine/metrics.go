package virtualmachine

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
)

// MetricsStorage implements GET <vm>/metrics. Returns a point-in-time
// sample from Proxmox's /status/current endpoint.
type MetricsStorage struct {
	resolver *proxmoxconn.Resolver
}

func NewMetricsStorage(r *proxmoxconn.Resolver) *MetricsStorage {
	return &MetricsStorage{resolver: r}
}

var (
	_ rest.Storage = (*MetricsStorage)(nil)
	_ rest.Getter  = (*MetricsStorage)(nil)
	_ rest.Scoper  = (*MetricsStorage)(nil)
)

func (s *MetricsStorage) New() runtime.Object   { return &pveopsv1alpha1.VirtualMachineMetrics{} }
func (s *MetricsStorage) Destroy()              {}
func (s *MetricsStorage) NamespaceScoped() bool { return false }

func (s *MetricsStorage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	target, err := s.resolver.ResolveVM(ctx, name)
	if err != nil {
		return nil, err
	}

	m, err := target.Client.GetVMMetrics(ctx, target.VMName, target.NodeName)
	if err != nil {
		return nil, err
	}

	return &pveopsv1alpha1.VirtualMachineMetrics{
		ObjectMeta:       metav1.ObjectMeta{Name: name},
		Timestamp:        metav1.Now(),
		CPUUsage:         m.CPUUsage,
		CPUCount:         m.CPUCount,
		MemoryUsedBytes:  m.MemoryUsedBytes,
		MemoryTotalBytes: m.MemoryTotalBytes,
		DiskReadBytes:    m.DiskReadBytes,
		DiskWriteBytes:   m.DiskWriteBytes,
		NetInBytes:       m.NetInBytes,
		NetOutBytes:      m.NetOutBytes,
		UptimeSeconds:    m.UptimeSeconds,
	}, nil
}
