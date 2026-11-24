package proxmox

import (
	"fmt"
	"testing"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const testMachine = "q35"

func optionMap(opts []proxmox.VirtualMachineOption) map[string]string {
	m := map[string]string{}
	for _, o := range opts {
		m[o.Name] = fmt.Sprintf("%v", o.Value)
	}
	return m
}

func TestFirmwareOptions(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Spec.VMSpec = &proxmoxv1alpha1.NewVMSpec{
		SMBIOSUUID: "0D9C1E2A-1111-4222-8333-444455556666",
		GuestAgent: true,
		Firmware: &proxmoxv1alpha1.VirtualMachineFirmware{
			Bios:           "ovmf",
			Machine:        testMachine,
			SCSIController: "virtio-scsi-single",
			EFIDisk:        &proxmoxv1alpha1.VirtualMachineEFIDisk{Storage: "local-zfs", PreEnrolledKeys: true},
			TPM:            &proxmoxv1alpha1.VirtualMachineTPM{Storage: "local-zfs"},
		},
	}
	got := optionMap(firmwareOptions(vm))
	want := map[string]string{
		"agent":     "1",
		"smbios1":   "uuid=0d9c1e2a-1111-4222-8333-444455556666",
		"bios":      "ovmf",
		"machine":   testMachine,
		"scsihw":    "virtio-scsi-single",
		"efidisk0":  "local-zfs:1,efitype=4m,pre-enrolled-keys=1",
		"tpmstate0": "local-zfs:1,version=v2.0",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("option %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("options = %v", got)
	}

	// Without ovmf the EFI disk is not created even if spelled out; without a
	// spec nothing is emitted.
	vm.Spec.VMSpec.Firmware.Bios = "seabios"
	if _, has := optionMap(firmwareOptions(vm))["efidisk0"]; has {
		t.Error("efidisk0 emitted for seabios")
	}
	if got := firmwareOptions(&proxmoxv1alpha1.VirtualMachine{}); len(got) != 0 {
		t.Errorf("bare options = %v", got)
	}
}

func TestFirmwareDrift(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Spec.VMSpec = &proxmoxv1alpha1.NewVMSpec{
		SMBIOSUUID: "0d9c1e2a-1111-4222-8333-444455556666",
		Firmware:   &proxmoxv1alpha1.VirtualMachineFirmware{Bios: "seabios", Machine: testMachine},
	}
	cfg := &proxmox.VirtualMachineConfig{
		SMBios1: "uuid=0D9C1E2A-1111-4222-8333-444455556666,manufacturer=QUJD",
		Machine: testMachine,
	}
	if drift := firmwareDrift(vm, cfg); len(drift) != 0 {
		t.Fatalf("no drift expected, got %v", optionMap(drift))
	}

	cfg.SMBios1 = "uuid=ffffffff-1111-4222-8333-444455556666"
	cfg.Machine = ""
	drift := optionMap(firmwareDrift(vm, cfg))
	if drift["smbios1"] != "uuid=0d9c1e2a-1111-4222-8333-444455556666" ||
		drift["machine"] != testMachine || len(drift) != 2 {
		t.Fatalf("drift = %v", drift)
	}
	if smbiosUUID("manufacturer=x") != "" {
		t.Fatal("smbiosUUID without uuid")
	}
}
