package proxmox

import (
	"fmt"
	"strings"

	"github.com/luthermonson/go-proxmox"
	"k8s.io/utils/ptr"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// The SMBIOS UUID and the firmware of a machine. The EFI vars disk and the TPM
// state disk are creation-time only: Proxmox has no cheap way to move them on
// a machine that exists. Bios, machine type, SCSI controller, guest agent and
// SMBIOS UUID are kept in line on every reconcile.

const (
	optionSMBIOS  = "smbios1"
	optionBios    = "bios"
	optionMachine = "machine"
	optionSCSIHW  = "scsihw"
	optionAgent   = "agent"
	optionEFIDisk = "efidisk0"
	optionTPM     = "tpmstate0"

	biosOVMF = "ovmf"

	firmwareTaskTimesNum = 5
	firmwareTaskSteps    = 10
)

// firmwareOptions are the options of a machine created from scratch: the
// kept options plus the two firmware disks.
func firmwareOptions(vm *proxmoxv1alpha1.VirtualMachine) []proxmox.VirtualMachineOption {
	opts := firmwareKeptOptions(vm)
	fw := firmwareOf(vm)
	if fw == nil {
		return opts
	}
	if fw.EFIDisk != nil && fw.Bios == biosOVMF {
		keys := 0
		if fw.EFIDisk.PreEnrolledKeys {
			keys = 1
		}
		opts = append(opts, proxmox.VirtualMachineOption{
			Name:  optionEFIDisk,
			Value: fmt.Sprintf("%s:1,efitype=4m,pre-enrolled-keys=%d", fw.EFIDisk.Storage, keys),
		})
	}
	if fw.TPM != nil {
		version := fw.TPM.Version
		if version == "" {
			version = "v2.0"
		}
		opts = append(opts, proxmox.VirtualMachineOption{
			Name:  optionTPM,
			Value: fmt.Sprintf("%s:1,version=%s", fw.TPM.Storage, version),
		})
	}
	return opts
}

// firmwareKeptOptions are the options kept in line on every reconcile. Each is
// only emitted when the spec sets it, so a machine without them keeps whatever
// Proxmox chose.
func firmwareKeptOptions(vm *proxmoxv1alpha1.VirtualMachine) []proxmox.VirtualMachineOption {
	opts := []proxmox.VirtualMachineOption{}
	if vm.Spec.VMSpec != nil && vm.Spec.VMSpec.GuestAgent {
		opts = append(opts, proxmox.VirtualMachineOption{Name: optionAgent, Value: "1"})
	}
	if vm.Spec.VMSpec != nil && vm.Spec.VMSpec.SMBIOSUUID != "" {
		opts = append(opts, proxmox.VirtualMachineOption{
			Name: optionSMBIOS, Value: "uuid=" + strings.ToLower(vm.Spec.VMSpec.SMBIOSUUID),
		})
	}
	if fw := firmwareOf(vm); fw != nil {
		if fw.Bios != "" {
			opts = append(opts, proxmox.VirtualMachineOption{Name: optionBios, Value: fw.Bios})
		}
		if fw.Machine != "" {
			opts = append(opts, proxmox.VirtualMachineOption{Name: optionMachine, Value: fw.Machine})
		}
		if fw.SCSIController != "" {
			opts = append(opts, proxmox.VirtualMachineOption{Name: optionSCSIHW, Value: fw.SCSIController})
		}
	}
	return opts
}

func firmwareOf(vm *proxmoxv1alpha1.VirtualMachine) *proxmoxv1alpha1.VirtualMachineFirmware {
	if vm.Spec.VMSpec == nil {
		return nil
	}
	return vm.Spec.VMSpec.Firmware
}

// firmwareDrift returns the kept options whose value on Proxmox differs from
// the spec. smbios1 is compared on its uuid only: Proxmox adds other fields of
// its own there.
func firmwareDrift(vm *proxmoxv1alpha1.VirtualMachine,
	cfg *proxmox.VirtualMachineConfig) []proxmox.VirtualMachineOption {
	drift := []proxmox.VirtualMachineOption{}
	for _, opt := range firmwareKeptOptions(vm) {
		want := fmt.Sprintf("%v", opt.Value)
		var have string
		switch opt.Name {
		case optionSMBIOS:
			have = "uuid=" + smbiosUUID(cfg.SMBios1)
		case optionBios:
			have = ptr.Deref(cfg.Bios, "seabios")
		case optionMachine:
			have = cfg.Machine
		case optionSCSIHW:
			have = ptr.Deref(cfg.SCSIHW, "lsi")
		case optionAgent:
			// Proxmox reports "1" or "enabled=1,..." for an enabled agent.
			have = "0"
			if a := cfg.Agent; a == "1" || strings.HasPrefix(a, "enabled=1") || strings.HasPrefix(a, "1,") {
				have = "1"
			}
		}
		if !strings.EqualFold(have, want) {
			drift = append(drift, opt)
		}
	}
	return drift
}

// smbiosUUID picks the uuid out of a smbios1 value such as
// "uuid=0d9c...,manufacturer=...".
func smbiosUUID(smbios1 string) string {
	for _, part := range strings.Split(smbios1, ",") {
		if strings.HasPrefix(part, "uuid=") {
			return strings.ToLower(strings.TrimPrefix(part, "uuid="))
		}
	}
	return ""
}

// ConfigureFirmware brings the SMBIOS UUID, BIOS, machine type, SCSI
// controller and guest agent of an existing machine in line with the spec. A
// changed value applies on the next boot; nothing here restarts the machine.
func (pc *ProxmoxClient) ConfigureFirmware(vm *proxmoxv1alpha1.VirtualMachine) error {
	virtualMachine, err := pc.getVirtualMachine(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	cfg := virtualMachine.VirtualMachineConfig
	if cfg == nil {
		return nil
	}
	drift := firmwareDrift(vm, cfg)
	if len(drift) == 0 {
		return nil
	}
	task, err := virtualMachine.Config(ctx, drift...)
	if err != nil {
		return fmt.Errorf("updating firmware of VirtualMachine %s: %w", vm.Spec.Name, err)
	}
	ok, _, taskErr := task.WaitForCompleteStatus(ctx, firmwareTaskTimesNum, firmwareTaskSteps)
	if !ok {
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if taskErr != nil {
		return fmt.Errorf("updating firmware of VirtualMachine %s: %w", vm.Spec.Name, taskErr)
	}
	return nil
}
