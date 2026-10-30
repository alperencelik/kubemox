package proxmox

import (
	"testing"

	"github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	scratchOSSlot   = "scsi0"
	scratchDataSlot = "scsi1"
	scratchStorage  = "local-zfs"
)

func scratchVM(osImage proxmoxv1alpha1.NewVMSpecOSImage) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name: "scratch",
			VMSpec: &proxmoxv1alpha1.NewVMSpec{
				Cores: 2, Socket: 1, Memory: 1024,
				OSImage: osImage,
				Disk: []proxmoxv1alpha1.VirtualMachineDisk{
					{Device: scratchOSSlot, VolumeRef: &corev1.LocalObjectReference{Name: "os"}},
					{Device: scratchDataSlot, Storage: scratchStorage, Size: 8},
				},
			},
		},
	}
}

func optionNames(opts []proxmox.VirtualMachineOption) []string {
	names := make([]string, 0, len(opts))
	for _, o := range opts {
		names = append(names, o.Name)
	}
	return names
}

func TestScratchCreateOptionsWithoutOSImage(t *testing.T) {
	opts := scratchCreateOptions(scratchVM(proxmoxv1alpha1.NewVMSpecOSImage{}))
	for _, o := range opts {
		if o.Name == "" {
			t.Fatalf("an option with an empty name was built: %v", optionNames(opts))
		}
		if o.Name == scratchOSSlot {
			t.Fatalf("the attached volume's slot must not be created with the VM: %v", optionNames(opts))
		}
	}
	want := []string{"sockets", "cores", "memory", "name", scratchDataSlot}
	if got := optionNames(opts); len(got) != len(want) {
		t.Fatalf("got options %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got options %v, want %v", got, want)
			}
		}
	}
}

func TestScratchCreateOptionsWithOSImage(t *testing.T) {
	opts := scratchCreateOptions(scratchVM(proxmoxv1alpha1.NewVMSpecOSImage{Name: "ide0", Value: "local:iso/debian.iso"}))
	for _, o := range opts {
		if o.Name == "ide0" {
			if o.Value != "local:iso/debian.iso,media=cdrom" {
				t.Fatalf("ide0 = %v", o.Value)
			}
			return
		}
	}
	t.Fatalf("the install medium was not built: %v", optionNames(opts))
}
