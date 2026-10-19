package proxmox

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// A VirtualMachine's drives come in two kinds: disks it owns, which the
// disk configuration below creates and resizes, and VirtualMachineVolumes
// attached by volumeRef, which belong to their holder and only visit the VM.
// The volume side is handled here; the own-disk code never sees a volumeRef
// drive, neither in the spec nor in the slots it occupies on Proxmox.

// OwnDisks returns the drives the VM owns: those without a volumeRef.
func OwnDisks(disks []proxmoxv1alpha1.VirtualMachineDisk) []proxmoxv1alpha1.VirtualMachineDisk {
	own := make([]proxmoxv1alpha1.VirtualMachineDisk, 0, len(disks))
	for _, d := range disks {
		if d.VolumeRef == nil {
			own = append(own, d)
		}
	}
	return own
}

// VolumeDisks returns the drives that attach a VirtualMachineVolume.
func VolumeDisks(disks []proxmoxv1alpha1.VirtualMachineDisk) []proxmoxv1alpha1.VirtualMachineDisk {
	refs := make([]proxmoxv1alpha1.VirtualMachineDisk, 0)
	for _, d := range disks {
		if d.VolumeRef != nil {
			refs = append(refs, d)
		}
	}
	return refs
}

// withoutVolumeDevices drops the slots that volumeRef drives occupy, and any
// further reserved slots, from the disks read off Proxmox, so that the
// own-disk comparison does not try to resize or delete a volume that merely
// visits the VM. The reserved slots matter for a volume whose ref the spec
// just dropped: it is still attached until the VM stops, and must not be
// removed as a stray disk meanwhile.
func withoutVolumeDevices(actual []proxmoxv1alpha1.VirtualMachineDisk,
	spec []proxmoxv1alpha1.VirtualMachineDisk, reserved ...string) []proxmoxv1alpha1.VirtualMachineDisk {
	skip := map[string]bool{}
	for _, d := range VolumeDisks(spec) {
		skip[d.Device] = true
	}
	for _, device := range reserved {
		skip[device] = true
	}
	kept := make([]proxmoxv1alpha1.VirtualMachineDisk, 0, len(actual))
	for _, d := range actual {
		if !skip[d.Device] {
			kept = append(kept, d)
		}
	}
	return kept
}

// VolumeStorage returns the storage part of a Proxmox volume id.
func VolumeStorage(volid string) string {
	return strings.SplitN(volid, ":", 2)[0]
}

// AttachVolume reassigns a volume from its holder to the VM's device. The
// slot has to be free; the volume comes back with the id Proxmox gave it
// under its new owner.
func (pc *ProxmoxClient) AttachVolume(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	device string, vol *proxmoxv1alpha1.VirtualMachineVolume) (volid string, err error) {
	if vol.Status.Holder == nil || vol.Status.Slot == "" {
		return "", fmt.Errorf("volume %s has no holder slot to attach from", vol.Name)
	}
	vmid, err := pc.getVMID(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return "", err
	}
	if vmid == 0 {
		return "", vmNotFound(vm)
	}
	if occupied, _, err := pc.ReadDisk(ctx, vm.Spec.NodeName, vmid, device); err != nil {
		return "", err
	} else if occupied != "" {
		if occupied == vol.Status.Volume {
			return occupied, nil
		}
		return "", fmt.Errorf("device %s of VirtualMachine %s already holds %s", device, vm.Spec.Name, occupied)
	}
	volid, err = pc.MoveDisk(ctx, vm.Spec.NodeName, vol.Status.Holder.VMID, vol.Status.Slot, vmid, device)
	if err != nil {
		return "", fmt.Errorf("attaching volume %s to %s %s: %w", vol.Name, vm.Spec.Name, device, err)
	}
	// The VM config changed behind the name cache.
	pc.invalidateVM(vm.Spec.NodeName, vmid)
	if err = pc.ensureBootable(ctx, vm.Spec.NodeName, vmid, device); err != nil {
		return volid, err
	}
	return volid, nil
}

// ensureBootable puts a freshly attached drive at the head of the boot order
// when it is not in it yet. Proxmox writes the boot order at creation from the
// devices present then; a volume attached afterwards, which is often the OS
// disk, would otherwise never be tried.
func (pc *ProxmoxClient) ensureBootable(ctx context.Context, node string, vmid int, device string) error {
	vm, err := pc.diskVM(ctx, node, vmid)
	if err != nil {
		return err
	}
	order := bootOrderWith(vm.VirtualMachineConfig.Boot, device)
	if order == vm.VirtualMachineConfig.Boot {
		return nil
	}
	task, err := vm.Config(ctx, proxmox.VirtualMachineOption{Name: "boot", Value: order})
	if err != nil {
		return fmt.Errorf("putting %s into the boot order of VM %d: %w", device, vmid, err)
	}
	if err = waitDiskTask(ctx, task, diskConfigTimeout); err != nil {
		return fmt.Errorf("putting %s into the boot order of VM %d: %w", device, vmid, err)
	}
	pc.invalidateVM(node, vmid)
	return nil
}

// bootOrderWith returns the boot order with device first, unchanged when the
// device is already listed. An empty or legacy (non "order=") value is
// replaced by one that only names the device.
func bootOrderWith(boot, device string) string {
	if !strings.HasPrefix(boot, "order=") {
		return "order=" + device
	}
	listed := strings.Split(strings.TrimPrefix(boot, "order="), ";")
	for _, d := range listed {
		if d == device {
			return boot
		}
	}
	return "order=" + strings.Join(append([]string{device}, listed...), ";")
}

// DetachVolume reassigns a volume from the VM's device back to its holder,
// into the first free slot there, and returns that slot and the volume's new
// id. The VM has to be stopped: Proxmox refuses to reassign a drive in use.
func (pc *ProxmoxClient) DetachVolume(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	device string, vol *proxmoxv1alpha1.VirtualMachineVolume) (holder *DiskHolder, slot, volid string, err error) {
	holder, err = pc.EnsureDiskHolder(ctx, vol.Namespace, vol.Spec.NodeName)
	if err != nil {
		return nil, "", "", err
	}
	vmid, err := pc.getVMID(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return nil, "", "", err
	}
	if vmid == 0 {
		return nil, "", "", vmNotFound(vm)
	}
	current, _, err := pc.ReadDisk(ctx, vm.Spec.NodeName, vmid, device)
	if err != nil {
		return nil, "", "", err
	}
	if current == "" {
		// The drive may have been unplugged behind the controller's back, by
		// hand or by an older operator; Proxmox then keeps the volume as an
		// unusedN entry of the same VM, which can still be given back.
		device, err = pc.unusedSlotOf(ctx, vm.Spec.NodeName, vmid, vol.Status.Volume)
		if err != nil {
			return nil, "", "", err
		}
		if device == "" {
			return nil, "", "", &NotFoundError{Message: fmt.Sprintf(
				"VirtualMachine %s has no drive holding volume %s any more", vm.Spec.Name, vol.Status.Volume)}
		}
	}

	unlock := lockHolder(holder.VMID)
	defer unlock()
	hvm, err := pc.diskVM(ctx, holder.Node, holder.VMID)
	if err != nil {
		return nil, "", "", err
	}
	slot, err = freeDiskSlot(hvm.VirtualMachineConfig)
	if err != nil {
		return nil, "", "", fmt.Errorf("holder %s (VMID %d): %w", holder.Name, holder.VMID, err)
	}
	volid, err = pc.MoveDisk(ctx, vm.Spec.NodeName, vmid, device, holder.VMID, slot)
	if err != nil {
		return nil, "", "", fmt.Errorf("detaching volume %s from %s %s: %w", vol.Name, vm.Spec.Name, device, err)
	}
	pc.invalidateVM(vm.Spec.NodeName, vmid)
	return holder, slot, volid, nil
}

func vmNotFound(vm *proxmoxv1alpha1.VirtualMachine) error {
	return &NotFoundError{Message: fmt.Sprintf("VirtualMachine %s not found on node %s", vm.Spec.Name, vm.Spec.NodeName)}
}

// unusedSlotOf returns the unusedN key of a VM that holds the volume, or ""
// when none does.
func (pc *ProxmoxClient) unusedSlotOf(ctx context.Context, node string, vmid int, volid string) (string, error) {
	if volid == "" {
		return "", nil
	}
	vm, err := pc.diskVM(ctx, node, vmid)
	if err != nil {
		return "", err
	}
	for key, value := range vm.VirtualMachineConfig.MergeUnuseds() {
		if unusedVolid, _ := parseDriveValue(value); unusedVolid == volid {
			return key, nil
		}
	}
	return "", nil
}

// invalidateVM drops a VM object from the name cache so the next read sees
// the config Proxmox has now.
func (pc *ProxmoxClient) invalidateVM(nodeName string, vmid int) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	if nc, ok := pc.nodesCache[nodeName]; ok && nc.vmObjs != nil {
		delete(nc.vmObjs, vmid)
	}
}
