package proxmox

import (
	"context"
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	"github.com/google/go-cmp/cmp"
	goproxmox "github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VirtualMachineKey holds the information needed to fetch a VM's state from Proxmox.
type VirtualMachineKey struct {
	ID            int
	NodeName      string
	ConnectionRef *corev1.LocalObjectReference
}

type VirtualMachineFetcher struct {
	Client client.Client
	pc     *proxmox.ProxmoxClient
}

func (f *VirtualMachineFetcher) GetDesiredState(ctx context.Context, key client.ObjectKey) (any, error) {
	var vm proxmoxv1alpha1.VirtualMachine
	return &vm, f.Client.Get(ctx, key, &vm)
}

func (f *VirtualMachineFetcher) FetchExternalResource(ctx context.Context, obj any) (any, error) {
	key, ok := obj.(VirtualMachineKey)
	if !ok {
		return nil, fmt.Errorf("expected VMCloudKey, got %T", obj)
	}

	// Setup the Proxmox client if not already done
	if f.pc == nil {
		pc, err := proxmox.NewProxmoxClientFromRef(ctx, f.Client, key.ConnectionRef)
		if err != nil {
			return nil, err
		}
		f.pc = pc
	}

	return f.pc.GetVirtualMachine(key.ID, key.NodeName)
}

func (f *VirtualMachineFetcher) IsResourceReadyToWatch(ctx context.Context, obj types.NamespacedName) bool {
	var vm proxmoxv1alpha1.VirtualMachine
	if err := f.Client.Get(ctx, obj, &vm); err != nil {
		return false
	}
	// Alternatively here we could check if the generation == observedGeneration

	// Check if the VM is ready to be watched
	if vm.Status.Status == nil || vm.Status.Status.ID == 0 {
		return false
	}

	return true
}

func (f *VirtualMachineFetcher) TransformExternalState(raw any) (any, error) {
	vm, ok := raw.(*goproxmox.VirtualMachine)

	if !ok {
		return nil, fmt.Errorf("expected *goproxmox.VirtualMachine, got %T", raw)
	}
	// Return the FetchedResource as a desired resource type
	return &proxmoxv1alpha1.VirtualMachine{
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Template: &proxmoxv1alpha1.VirtualMachineSpecTemplate{
				Cores:  vm.CPUs,
				Memory: int(vm.VirtualMachineConfig.Memory),
			},
		},
	}, nil
}

type VirtualMachineComparator struct{}

func (vmc VirtualMachineComparator) Compare(desired, actual any) (bool, error) {
	// Both object are expected to be of type *proxmoxv1alpha1.VirtualMachine
	if desiredVM, ok := desired.(*proxmoxv1alpha1.VirtualMachine); ok {
		if actualVM, ok := actual.(*proxmoxv1alpha1.VirtualMachine); ok {
			// Use cmp.Equal to compare the relevant fields of the desired and actual VM states
			// This example is comparing the Cores field, but you can compare any field depending on your requirements
			return !cmp.Equal(desiredVM.Spec.Template.Cores, actualVM.Spec.Template.Cores, cmp.AllowUnexported(proxmoxv1alpha1.VirtualMachine{})), nil
		}
		return false, fmt.Errorf("expected *proxmoxv1alpha1.VirtualMachine, got %T", actual)
	}
	return false, fmt.Errorf("expected *proxmoxv1alpha1.VirtualMachine, got %T", desired)
}

func (vmc VirtualMachineComparator) HasDrifted(desired, actual any) (bool, error) {
	drifted, err := vmc.Compare(desired, actual)
	if err != nil {
		return false, fmt.Errorf("failed to compare VM states: %w", err)
	}
	if drifted {
		fmt.Println("VM state has drifted")
	}
	return drifted, nil
}
