package proxmox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestStaleVolumes(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Name = "web"
	vm.Spec.VMSpec = &proxmoxv1alpha1.NewVMSpec{Disk: []proxmoxv1alpha1.VirtualMachineDisk{
		{Device: "scsi0", Storage: "local-zfs", Size: 8},
		{Device: "scsi1", VolumeRef: &corev1.LocalObjectReference{Name: "data"}},
		{Device: "scsi2", VolumeRef: &corev1.LocalObjectReference{Name: "moved"}},
	}}
	mk := func(name, attachedTo, slot string) proxmoxv1alpha1.VirtualMachineVolume {
		v := proxmoxv1alpha1.VirtualMachineVolume{}
		v.Name = name
		v.Status.AttachedTo = attachedTo
		v.Status.Slot = slot
		return v
	}
	attached := []proxmoxv1alpha1.VirtualMachineVolume{
		mk("data", "web", "scsi1"),    // wanted where it is
		mk("moved", "web", "scsi3"),   // wanted, but at another device
		mk("dropped", "web", "scsi4"), // no longer in the spec
		mk("theirs", "db", "scsi1"),   // another VM's
	}
	stale := staleVolumes(vm, attached)
	got := map[string]bool{}
	for _, v := range stale {
		got[v.Name] = true
	}
	if len(got) != 2 || !got["moved"] || !got["dropped"] {
		t.Fatalf("staleVolumes = %v", got)
	}
	if w := wantedVolumes(vm); len(w) != 2 || w["data"] != "scsi1" || w["moved"] != "scsi2" {
		t.Fatalf("wantedVolumes = %v", w)
	}
}
