package proxmox

import (
	"context"
	"fmt"
	"strings"

	"github.com/alperencelik/kube-external-watcher/watcher"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	gopxmx "github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Resource is a Kubernetes client.Object used for Proxmox resource operations.
type Resource interface {
	cc.Object
}

// ManagedVMDesiredState represents the comparable desired/actual state for a ManagedVirtualMachine.
type ManagedVMDesiredState struct {
	Cores  int
	Memory int
}

// ContainerDesiredState represents the comparable desired/actual state for a Container.
type ContainerDesiredState struct {
	Cores  int
	Memory int
}

// VMTemplateDesiredState represents the comparable desired/actual state for a VirtualMachineTemplate.
type VMTemplateDesiredState struct {
	Cores   int
	Sockets int
	Memory  int
}

// ProxmoxResourceKey holds the information needed to fetch a resource from Proxmox.
type ProxmoxResourceKey struct {
	Name          string
	NodeName      string
	ConnectionRef *corev1.LocalObjectReference
}

// --- VirtualMachine Fetcher ---

// VirtualMachineFetcher implements watcher.ResourceStateFetcher for VirtualMachine resources.
type VirtualMachineFetcher struct {
	Client cc.Client
}

func (f *VirtualMachineFetcher) GetDesiredState(ctx context.Context, key types.NamespacedName) (any, error) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		return nil, err
	}
	// Skip drift detection for WatchOnly and EnsureExists modes
	reconcileMode := kubernetes.GetReconcileMode(vm)
	if reconcileMode == kubernetes.ReconcileModeWatchOnly || reconcileMode == kubernetes.ReconcileModeEnsureExists {
		return nil, nil
	}
	return VirtualMachineComparison{
		Cores:    GetCores(vm),
		Sockets:  getSockets(vm),
		Memory:   GetMemory(vm),
		Networks: getNetworks(vm),
		Disks:    sortDisks(getDisks(vm)),
	}, nil
}

func (f *VirtualMachineFetcher) FetchExternalResource(ctx context.Context, objKey any) (any, error) {
	key, ok := objKey.(ProxmoxResourceKey)
	if !ok {
		return nil, fmt.Errorf("unexpected resource key type: %T", objKey)
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, key.ConnectionRef)
	if err != nil {
		return nil, fmt.Errorf("getting Proxmox client: %w", err)
	}
	// Fetch fresh VM data from Proxmox API (bypass cache)
	node, err := pc.getNode(ctx, key.NodeName)
	if err != nil {
		return nil, err
	}
	vmID, err := pc.getVMID(key.Name, key.NodeName)
	if err != nil {
		return nil, err
	}
	vm, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return nil, err
	}
	pc.setCachedVM(key.NodeName, vmID, vm)
	return vm, nil
}

func (f *VirtualMachineFetcher) TransformExternalState(raw any) (any, error) {
	vm, ok := raw.(*gopxmx.VirtualMachine)
	if !ok {
		return nil, fmt.Errorf("unexpected external state type: %T", raw)
	}
	cfg := vm.VirtualMachineConfig

	networks, err := parseNetworkConfiguration(cfg.MergeNets())
	if err != nil {
		return nil, err
	}

	disks := cfg.MergeDisks()
	for k, v := range disks {
		if strings.Contains(v, "media=cdrom") {
			delete(disks, k)
		}
	}
	parsedDisks, err := parseDiskConfiguration(disks)
	if err != nil {
		return nil, err
	}

	return VirtualMachineComparison{
		Cores:    cfg.Cores,
		Sockets:  cfg.Sockets,
		Memory:   int(cfg.Memory),
		Networks: networks,
		Disks:    sortDisks(parsedDisks),
	}, nil
}

func (f *VirtualMachineFetcher) IsResourceReadyToWatch(ctx context.Context, key types.NamespacedName) bool {
	logger := log.FromContext(ctx).WithValues("resource", key.String())
	vm := &proxmoxv1alpha1.VirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		logger.V(2).Info("readiness check: failed to get CR", "error", err)
		return false
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, vm.Spec.ConnectionRef)
	if err != nil {
		logger.V(2).Info("readiness check: failed to get Proxmox client", "error", err)
		return false
	}
	ready, err := pc.IsVirtualMachineReady(vm)
	if err != nil {
		logger.V(2).Info("readiness check: VM not ready", "vmName", vm.Spec.Name, "error", err)
		return false
	}
	if !ready {
		logger.V(2).Info("readiness check: VM not ready (no VMID or locked)", "vmName", vm.Spec.Name)
	}
	return ready
}

func (f *VirtualMachineFetcher) UpdateResourceStatus(ctx context.Context, key types.NamespacedName, _ any) error {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		return err
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, vm.Spec.ConnectionRef)
	if err != nil {
		return err
	}
	qemuStatus, err := pc.UpdateVMStatus(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	vm.Status.Status = qemuStatus
	return f.Client.Status().Update(ctx, vm)
}

// --- ManagedVirtualMachine Fetcher ---

// ManagedVirtualMachineFetcher implements watcher.ResourceStateFetcher for ManagedVirtualMachine resources.
type ManagedVirtualMachineFetcher struct {
	Client cc.Client
}

func (f *ManagedVirtualMachineFetcher) GetDesiredState(ctx context.Context, key types.NamespacedName) (any, error) {
	vm := &proxmoxv1alpha1.ManagedVirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		return nil, err
	}
	reconcileMode := kubernetes.GetReconcileMode(vm)
	if reconcileMode == kubernetes.ReconcileModeWatchOnly || reconcileMode == kubernetes.ReconcileModeEnsureExists {
		return nil, nil
	}
	return ManagedVMDesiredState{
		Cores:  vm.Spec.Cores,
		Memory: vm.Spec.Memory,
	}, nil
}

func (f *ManagedVirtualMachineFetcher) FetchExternalResource(ctx context.Context, objKey any) (any, error) {
	key, ok := objKey.(ProxmoxResourceKey)
	if !ok {
		return nil, fmt.Errorf("unexpected resource key type: %T", objKey)
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, key.ConnectionRef)
	if err != nil {
		return nil, fmt.Errorf("getting Proxmox client: %w", err)
	}
	vm, err := pc.getVirtualMachine(key.Name, key.NodeName)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

func (f *ManagedVirtualMachineFetcher) TransformExternalState(raw any) (any, error) {
	vm, ok := raw.(*gopxmx.VirtualMachine)
	if !ok {
		return nil, fmt.Errorf("unexpected external state type: %T", raw)
	}
	return ManagedVMDesiredState{
		Cores:  vm.VirtualMachineConfig.Cores,
		Memory: int(vm.VirtualMachineConfig.Memory),
	}, nil
}

func (f *ManagedVirtualMachineFetcher) IsResourceReadyToWatch(ctx context.Context, key types.NamespacedName) bool {
	logger := log.FromContext(ctx).WithValues("resource", key.String())
	vm := &proxmoxv1alpha1.ManagedVirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		logger.V(2).Info("readiness check: failed to get CR", "error", err)
		return false
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, vm.Spec.ConnectionRef)
	if err != nil {
		logger.V(2).Info("readiness check: failed to get Proxmox client", "error", err)
		return false
	}
	ready, err := pc.IsVirtualMachineReady(vm)
	if err != nil {
		logger.V(2).Info("readiness check: VM not ready", "vmName", vm.Spec.Name, "error", err)
		return false
	}
	if !ready {
		logger.V(2).Info("readiness check: VM not ready (no VMID or locked)", "vmName", vm.Spec.Name)
	}
	return ready
}

func (f *ManagedVirtualMachineFetcher) UpdateResourceStatus(ctx context.Context,
	key types.NamespacedName, _ any) error {
	vm := &proxmoxv1alpha1.ManagedVirtualMachine{}
	if err := f.Client.Get(ctx, key, vm); err != nil {
		return err
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, vm.Spec.ConnectionRef)
	if err != nil {
		return err
	}
	nodeName, err := pc.GetNodeOfVM(vm.Name)
	if err != nil {
		return err
	}
	qemuStatus, err := pc.UpdateVMStatus(vm.Name, nodeName)
	if err != nil {
		return err
	}
	vm.Status.Status = qemuStatus
	return f.Client.Status().Update(ctx, vm)
}

// --- Container Fetcher ---

// ContainerFetcher implements watcher.ResourceStateFetcher for Container resources.
type ContainerFetcher struct {
	Client cc.Client
}

func (f *ContainerFetcher) GetDesiredState(ctx context.Context, key types.NamespacedName) (any, error) {
	container := &proxmoxv1alpha1.Container{}
	if err := f.Client.Get(ctx, key, container); err != nil {
		return nil, err
	}
	reconcileMode := kubernetes.GetReconcileMode(container)
	if reconcileMode == kubernetes.ReconcileModeWatchOnly || reconcileMode == kubernetes.ReconcileModeEnsureExists {
		return nil, nil
	}
	return ContainerDesiredState{
		Cores:  container.Spec.Template.Cores,
		Memory: container.Spec.Template.Memory,
	}, nil
}

func (f *ContainerFetcher) FetchExternalResource(ctx context.Context, objKey any) (any, error) {
	key, ok := objKey.(ProxmoxResourceKey)
	if !ok {
		return nil, fmt.Errorf("unexpected resource key type: %T", objKey)
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, key.ConnectionRef)
	if err != nil {
		return nil, fmt.Errorf("getting Proxmox client: %w", err)
	}
	container, err := pc.GetContainer(key.Name, key.NodeName)
	if err != nil {
		return nil, err
	}
	return container, nil
}

func (f *ContainerFetcher) TransformExternalState(raw any) (any, error) {
	container, ok := raw.(*gopxmx.Container)
	if !ok {
		return nil, fmt.Errorf("unexpected external state type: %T", raw)
	}
	return ContainerDesiredState{
		Cores:  container.CPUs,
		Memory: int(container.MaxMem / 1024 / 1024),
	}, nil
}

func (f *ContainerFetcher) IsResourceReadyToWatch(ctx context.Context, key types.NamespacedName) bool {
	logger := log.FromContext(ctx).WithValues("resource", key.String())
	container := &proxmoxv1alpha1.Container{}
	if err := f.Client.Get(ctx, key, container); err != nil {
		logger.V(2).Info("readiness check: failed to get CR", "error", err)
		return false
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, container.Spec.ConnectionRef)
	if err != nil {
		logger.V(2).Info("readiness check: failed to get Proxmox client", "error", err)
		return false
	}
	ready, err := pc.IsContainerReady(container)
	if err != nil {
		logger.V(2).Info("readiness check: container not ready", "containerName", container.Spec.Name, "error", err)
		return false
	}
	if !ready {
		logger.V(2).Info("readiness check: container not ready (not found or locked)", "containerName", container.Spec.Name)
	}
	return ready
}

func (f *ContainerFetcher) UpdateResourceStatus(ctx context.Context, key types.NamespacedName, _ any) error {
	container := &proxmoxv1alpha1.Container{}
	if err := f.Client.Get(ctx, key, container); err != nil {
		return err
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, container.Spec.ConnectionRef)
	if err != nil {
		return err
	}
	containerStatus, err := pc.UpdateContainerStatus(container.Spec.Name, container.Spec.NodeName)
	if err != nil {
		return err
	}
	container.Status.Status = proxmoxv1alpha1.QEMUStatus{
		State:  containerStatus.State,
		Node:   containerStatus.Node,
		Uptime: containerStatus.Uptime,
		ID:     containerStatus.ID,
	}
	return f.Client.Status().Update(ctx, container)
}

// --- VirtualMachineTemplate Fetcher ---

// VirtualMachineTemplateFetcher implements watcher.ResourceStateFetcher for VirtualMachineTemplate resources.
type VirtualMachineTemplateFetcher struct {
	Client cc.Client
}

func (f *VirtualMachineTemplateFetcher) GetDesiredState(ctx context.Context, key types.NamespacedName) (any, error) {
	vmTemplate := &proxmoxv1alpha1.VirtualMachineTemplate{}
	if err := f.Client.Get(ctx, key, vmTemplate); err != nil {
		return nil, err
	}
	reconcileMode := kubernetes.GetReconcileMode(vmTemplate)
	if reconcileMode == kubernetes.ReconcileModeWatchOnly || reconcileMode == kubernetes.ReconcileModeEnsureExists {
		return nil, nil
	}
	return VMTemplateDesiredState{
		Cores:   vmTemplate.Spec.VirtualMachineConfig.Cores,
		Sockets: vmTemplate.Spec.VirtualMachineConfig.Sockets,
		Memory:  vmTemplate.Spec.VirtualMachineConfig.Memory,
	}, nil
}

func (f *VirtualMachineTemplateFetcher) FetchExternalResource(ctx context.Context, objKey any) (any, error) {
	key, ok := objKey.(ProxmoxResourceKey)
	if !ok {
		return nil, fmt.Errorf("unexpected resource key type: %T", objKey)
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, key.ConnectionRef)
	if err != nil {
		return nil, fmt.Errorf("getting Proxmox client: %w", err)
	}
	vm, err := pc.getVirtualMachine(key.Name, key.NodeName)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

func (f *VirtualMachineTemplateFetcher) TransformExternalState(raw any) (any, error) {
	vm, ok := raw.(*gopxmx.VirtualMachine)
	if !ok {
		return nil, fmt.Errorf("unexpected external state type: %T", raw)
	}
	cfg := vm.VirtualMachineConfig
	return VMTemplateDesiredState{
		Cores:   cfg.Cores,
		Sockets: cfg.Sockets,
		Memory:  int(cfg.Memory),
	}, nil
}

func (f *VirtualMachineTemplateFetcher) IsResourceReadyToWatch(ctx context.Context, key types.NamespacedName) bool {
	logger := log.FromContext(ctx).WithValues("resource", key.String())
	vmTemplate := &proxmoxv1alpha1.VirtualMachineTemplate{}
	if err := f.Client.Get(ctx, key, vmTemplate); err != nil {
		logger.V(2).Info("readiness check: failed to get CR", "error", err)
		return false
	}
	pc, err := NewProxmoxClientFromRef(ctx, f.Client, vmTemplate.Spec.ConnectionRef)
	if err != nil {
		logger.V(2).Info("readiness check: failed to get Proxmox client", "error", err)
		return false
	}
	ready, err := pc.IsVirtualMachineReady(vmTemplate)
	if err != nil {
		logger.V(2).Info("readiness check: VMTemplate not ready", "vmName", vmTemplate.Spec.Name, "error", err)
		return false
	}
	if !ready {
		logger.V(2).Info("readiness check: VMTemplate not ready (no VMID or locked)", "vmName", vmTemplate.Spec.Name)
	}
	return ready
}

// --- ConfigExtractorFn implementations for auto-register ---

// VMConfigExtractor extracts watcher.ResourceConfig from a VirtualMachine object.
func VMConfigExtractor(obj cc.Object) watcher.ResourceConfig {
	vm := obj.(*proxmoxv1alpha1.VirtualMachine)
	return watcher.ResourceConfig{
		ResourceKey: ProxmoxResourceKey{
			Name:          vm.Spec.Name,
			NodeName:      vm.Spec.NodeName,
			ConnectionRef: vm.Spec.ConnectionRef,
		},
	}
}

// ManagedVMConfigExtractor extracts watcher.ResourceConfig from a ManagedVirtualMachine object.
func ManagedVMConfigExtractor(obj cc.Object) watcher.ResourceConfig {
	vm := obj.(*proxmoxv1alpha1.ManagedVirtualMachine)
	return watcher.ResourceConfig{
		ResourceKey: ProxmoxResourceKey{
			Name:          vm.Spec.Name,
			NodeName:      vm.Spec.NodeName,
			ConnectionRef: vm.Spec.ConnectionRef,
		},
	}
}

// ContainerConfigExtractor extracts watcher.ResourceConfig from a Container object.
func ContainerConfigExtractor(obj cc.Object) watcher.ResourceConfig {
	container := obj.(*proxmoxv1alpha1.Container)
	return watcher.ResourceConfig{
		ResourceKey: ProxmoxResourceKey{
			Name:          container.Spec.Name,
			NodeName:      container.Spec.NodeName,
			ConnectionRef: container.Spec.ConnectionRef,
		},
	}
}

// VMTemplateConfigExtractor extracts watcher.ResourceConfig from a VirtualMachineTemplate object.
func VMTemplateConfigExtractor(obj cc.Object) watcher.ResourceConfig {
	vmTemplate := obj.(*proxmoxv1alpha1.VirtualMachineTemplate)
	return watcher.ResourceConfig{
		ResourceKey: ProxmoxResourceKey{
			Name:          vmTemplate.Spec.Name,
			NodeName:      vmTemplate.Spec.NodeName,
			ConnectionRef: vmTemplate.Spec.ConnectionRef,
		},
	}
}
