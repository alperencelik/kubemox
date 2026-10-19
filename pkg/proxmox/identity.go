package proxmox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// The pool, the tags, the SMBIOS UUID and the firmware of a machine. Pool
// and the two firmware disks are creation-time only: Proxmox has no cheap way
// to move a running machine's EFI vars or TPM state, and a pool change is a
// pool-membership call rather than a machine config. Bios, machine type,
// SMBIOS UUID and tags are kept in line on every reconcile.

const (
	optionPool    = "pool"
	optionTags    = "tags"
	optionSMBIOS  = "smbios1"
	optionBios    = "bios"
	optionMachine = "machine"
	optionSCSIHW  = "scsihw"
	optionAgent   = "agent"
	optionEFIDisk = "efidisk0"
	optionTPM     = "tpmstate0"

	identityConfigTimeout = 5 * time.Minute
	identityPollInterval  = 2 * time.Second
)

// creationOptions are the options of a machine created from scratch that
// only make sense at creation: pool, tags and the firmware disks, plus the
// identity options that are also kept afterwards.
func creationOptions(vm *proxmoxv1alpha1.VirtualMachine) []proxmox.VirtualMachineOption {
	opts := []proxmox.VirtualMachineOption{}
	if vm.Spec.Pool != "" {
		opts = append(opts, proxmox.VirtualMachineOption{Name: optionPool, Value: vm.Spec.Pool})
	}
	if tags := desiredTags(vm); len(tags) > 0 {
		opts = append(opts, proxmox.VirtualMachineOption{Name: optionTags, Value: strings.Join(tags, ";")})
	}
	opts = append(opts, identityOptions(vm)...)
	if fw := firmwareOf(vm); fw != nil {
		if fw.EFIDisk != nil && fw.Bios == "ovmf" {
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
	}
	return opts
}

// identityOptions are the options kept in line on every reconcile: the
// SMBIOS UUID, the BIOS and the machine type. Each is only emitted when the
// spec sets it, so a machine without them keeps whatever Proxmox chose.
func identityOptions(vm *proxmoxv1alpha1.VirtualMachine) []proxmox.VirtualMachineOption {
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

// desiredTags are the spec's tags plus the operator's own, deduplicated and
// sorted so that the comparison with Proxmox is stable.
func desiredTags(vm *proxmoxv1alpha1.VirtualMachine) []string {
	seen := map[string]bool{virtualMachineTag: true}
	tags := []string{virtualMachineTag}
	for _, t := range vm.Spec.Tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// identityDrift returns the identity options whose value on Proxmox differs
// from the spec. smbios1 is compared on its uuid only: Proxmox adds other
// fields of its own there.
func identityDrift(vm *proxmoxv1alpha1.VirtualMachine,
	cfg *proxmox.VirtualMachineConfig) []proxmox.VirtualMachineOption {
	drift := []proxmox.VirtualMachineOption{}
	for _, opt := range identityOptions(vm) {
		want := fmt.Sprintf("%v", opt.Value)
		var have string
		switch opt.Name {
		case optionSMBIOS:
			have = "uuid=" + smbiosUUID(cfg.SMBios1)
		case optionBios:
			have = cfg.Bios
			if have == "" {
				have = "seabios"
			}
		case optionMachine:
			have = cfg.Machine
		case optionSCSIHW:
			have = cfg.SCSIHW
			if have == "" {
				have = "lsi"
			}
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

// missingTags are the desired tags the machine does not carry yet.
func missingTags(vm *proxmoxv1alpha1.VirtualMachine, cfg *proxmox.VirtualMachineConfig) []string {
	have := map[string]bool{}
	for _, t := range strings.Split(cfg.Tags, ";") {
		if t != "" {
			have[t] = true
		}
	}
	missing := []string{}
	for _, t := range desiredTags(vm) {
		if !have[t] {
			missing = append(missing, t)
		}
	}
	return missing
}

// ConfigureIdentity brings the SMBIOS UUID, BIOS, machine type and tags of
// an existing machine in line with the spec. A changed BIOS or UUID applies
// on the next boot; nothing here restarts the machine.
func (pc *ProxmoxClient) ConfigureIdentity(vm *proxmoxv1alpha1.VirtualMachine) error {
	virtualMachine, err := pc.getVirtualMachine(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	cfg := virtualMachine.VirtualMachineConfig
	if cfg == nil {
		return nil
	}
	if drift := identityDrift(vm, cfg); len(drift) > 0 {
		task, err := virtualMachine.Config(ctx, drift...)
		if err != nil {
			return fmt.Errorf("updating identity of VirtualMachine %s: %w", vm.Spec.Name, err)
		}
		if err = waitConfigTask(ctx, task, identityConfigTimeout); err != nil {
			return fmt.Errorf("updating identity of VirtualMachine %s: %w", vm.Spec.Name, err)
		}
	}
	for _, tag := range missingTags(vm, cfg) {
		task, err := virtualMachine.AddTag(ctx, tag)
		if err != nil {
			if proxmox.IsErrNoop(err) {
				continue
			}
			return fmt.Errorf("adding tag %q to VirtualMachine %s: %w", tag, vm.Spec.Name, err)
		}
		if err = waitConfigTask(ctx, task, identityConfigTimeout); err != nil {
			return fmt.Errorf("adding tag %q to VirtualMachine %s: %w", tag, vm.Spec.Name, err)
		}
	}
	return nil
}

// waitConfigTask waits for a config task and turns a failed one into a
// TaskError. A nil task is a change Proxmox applied synchronously.
func waitConfigTask(ctx context.Context, task *proxmox.Task, timeout time.Duration) error {
	if task == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := task.Ping(ctx); err != nil {
			return fmt.Errorf("polling task %s: %w", task.UPID, err)
		}
		if task.IsCompleted {
			if !task.IsSuccessful {
				return &TaskError{ExitStatus: task.ExitStatus}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("task %s still running after %s", task.UPID, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(identityPollInterval):
		}
	}
}
