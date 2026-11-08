package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// A VirtualMachineVolume is a volume that outlives the VM using it. Proxmox has
// no standalone volume: every volume belongs to a VMID, and destroying a VM
// destroys the volumes still attached to it. The disk therefore lives on a
// holder, a VM per namespace and node that never starts, and is reassigned
// between the holder and a VirtualMachine with move_disk. Reassignment only
// works between VMs on the same node.
//
// The holder keeps its disks attached as regular slots rather than as
// unusedN entries: with protection enabled Proxmox refuses to detach a
// drive, but still allows reassigning one, and protection is what keeps a
// stray `qm destroy` from taking every tenant volume with it.

const (
	// DiskHolderTag marks the holder VMs, next to the operator tag.
	DiskHolderTag = "kubemox-disk-holder"

	diskHolderPrefix   = "kubemox-disks"
	diskHolderMemoryMB = 128
	// proxmoxNameMaxLen is the longest name Proxmox accepts for a VM.
	proxmoxNameMaxLen = 63

	// configKeyName is the Proxmox config key of a VM's name.
	configKeyName = "name"

	// diskTaskPollInterval is how often a disk task is polled.
	diskTaskPollInterval = 3 * time.Second
	// diskConfigTimeout bounds the config changes that do not copy data.
	diskConfigTimeout = 5 * time.Minute
	// diskImportTimeout bounds import-from, which copies the whole source.
	diskImportTimeout = 2 * time.Hour
)

// diskHolderSlots are the drive slots a holder hands out, in order. SCSI
// first because there are the most of them; the holder never boots, so the
// bus does not matter to it.
var diskHolderSlots = func() []string {
	slots := make([]string, 0, 31+16+6)
	for i := 0; i <= 30; i++ {
		slots = append(slots, fmt.Sprintf("scsi%d", i))
	}
	for i := 0; i <= 15; i++ {
		slots = append(slots, fmt.Sprintf("virtio%d", i))
	}
	for i := 0; i <= 5; i++ {
		slots = append(slots, fmt.Sprintf("sata%d", i))
	}
	return slots
}()

// holderLocks serialises slot allocation per holder VMID: two disks picking
// the same free slot would make the second config write replace the first
// volume, and Proxmox does not refuse that.
var holderLocks sync.Map

func lockHolder(vmid int) func() {
	m, _ := holderLocks.LoadOrStore(vmid, &sync.Mutex{})
	mu := m.(*sync.Mutex) //nolint:forcetypeassert // only *sync.Mutex is ever stored
	mu.Lock()
	return mu.Unlock
}

// DiskHolder identifies the VM that keeps the volumes of a namespace on a node.
type DiskHolder struct {
	Name string
	VMID int
	Node string
}

// HolderName is the Proxmox VM name of the holder for a namespace on a node.
// Namespace and node are DNS labels already; the name is only shortened, with
// a hash to keep it unique, when the two together exceed what Proxmox takes.
func HolderName(namespace, node string) string {
	name := fmt.Sprintf("%s-%s-%s", diskHolderPrefix, namespace, node)
	if len(name) <= proxmoxNameMaxLen {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := hex.EncodeToString(sum[:])[:8]
	return name[:proxmoxNameMaxLen-len(suffix)-1] + "-" + suffix
}

// FindDiskHolder returns the holder of a namespace on a node, or nil when
// none exists yet.
func (pc *ProxmoxClient) FindDiskHolder(ctx context.Context, namespace, node string) (*DiskHolder, error) {
	n, err := pc.getNode(ctx, node)
	if err != nil {
		return nil, err
	}
	vms, err := n.VirtualMachines(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing VMs on node %s: %w", node, err)
	}
	want := HolderName(namespace, node)
	for _, vm := range vms {
		if vm.Name == want {
			return &DiskHolder{Name: want, VMID: int(vm.VMID), Node: node}, nil
		}
	}
	return nil, nil //nolint:nilnil // nil holder means none exists; the error is for the API
}

// EnsureDiskHolder returns the holder of a namespace on a node, creating it
// when it does not exist. The holder gets no NIC, the smallest memory Proxmox
// accepts and protection, and nothing ever starts it.
func (pc *ProxmoxClient) EnsureDiskHolder(ctx context.Context, namespace, node string) (*DiskHolder, error) {
	if h, err := pc.FindDiskHolder(ctx, namespace, node); err != nil || h != nil {
		return h, err
	}
	n, err := pc.getNode(ctx, node)
	if err != nil {
		return nil, err
	}
	name := HolderName(namespace, node)
	options := []proxmox.VirtualMachineOption{
		{Name: configKeyName, Value: name},
		{Name: "memory", Value: diskHolderMemoryMB},
		{Name: "cores", Value: 1},
		{Name: "protection", Value: 1},
		{Name: "scsihw", Value: "virtio-scsi-single"},
		{Name: "tags", Value: virtualMachineTag + ";" + DiskHolderTag},
		{Name: "description", Value: fmt.Sprintf(
			"Disk holder managed by kubemox for namespace %s on node %s. "+
				"It never starts. Its drives are VirtualMachineVolume objects; "+
				"do not delete it or detach them by hand.", namespace, node)},
	}

	var (
		vmid int
		task *proxmox.Task
	)
	// Same lock as VM creation: two callers must not get the same next VMID.
	func() {
		mutex.Lock()
		defer mutex.Unlock()
		vmid, err = getNextVMID(pc.Client)
		if err != nil {
			return
		}
		task, err = n.NewVirtualMachine(ctx, vmid, options...)
	}()
	if err != nil {
		return nil, fmt.Errorf("creating disk holder %s on node %s: %w", name, node, err)
	}
	if err = waitDiskTask(ctx, task, diskConfigTimeout); err != nil {
		return nil, fmt.Errorf("creating disk holder %s on node %s: %w", name, node, err)
	}
	log.FromContext(ctx).Info("Created disk holder", "name", name, "node", node, "vmid", vmid)
	return &DiskHolder{Name: name, VMID: vmid, Node: node}, nil
}

// diskVM reads a VM by VMID, bypassing the name cache: a holder is addressed
// by VMID and its config is what the disk operations work on.
func (pc *ProxmoxClient) diskVM(ctx context.Context, node string, vmid int) (*proxmox.VirtualMachine, error) {
	n, err := pc.getNode(ctx, node)
	if err != nil {
		return nil, err
	}
	vm, err := n.VirtualMachine(ctx, vmid)
	if err != nil {
		return nil, fmt.Errorf("reading VM %d on node %s: %w", vmid, node, err)
	}
	return vm, nil
}

// CreateHolderDisk allocates a volume on the holder: blank when importFrom
// is empty, otherwise a copy of that Proxmox volume id made by import-from.
// A copy that comes out smaller than sizeGiB is grown to it. It returns the
// slot the volume sits in, its volume id and its size.
func (pc *ProxmoxClient) CreateHolderDisk(ctx context.Context, h *DiskHolder,
	storage string, sizeGiB int, importFrom string) (slot, volid string, actualGiB int, err error) {
	unlock := lockHolder(h.VMID)
	defer unlock()

	vm, err := pc.diskVM(ctx, h.Node, h.VMID)
	if err != nil {
		return "", "", 0, err
	}
	slot, err = freeDiskSlot(vm.VirtualMachineConfig)
	if err != nil {
		return "", "", 0, fmt.Errorf("holder %s (VMID %d): %w", h.Name, h.VMID, err)
	}

	value := fmt.Sprintf("%s:%d", storage, sizeGiB)
	timeout := diskConfigTimeout
	if importFrom != "" {
		value = fmt.Sprintf("%s:0,import-from=%s", storage, importFrom)
		timeout = diskImportTimeout
	}
	task, err := vm.Config(ctx, proxmox.VirtualMachineOption{Name: slot, Value: value})
	if err != nil {
		return "", "", 0, fmt.Errorf("creating disk %s on holder %d: %w", slot, h.VMID, err)
	}
	if err = waitDiskTask(ctx, task, timeout); err != nil {
		return "", "", 0, fmt.Errorf("creating disk %s on holder %d: %w", slot, h.VMID, err)
	}

	volid, actualGiB, err = pc.ReadDisk(ctx, h.Node, h.VMID, slot)
	if err != nil {
		return "", "", 0, err
	}
	if importFrom != "" && actualGiB < sizeGiB {
		if err = pc.ResizeDisk(ctx, h.Node, h.VMID, slot, sizeGiB); err != nil {
			return slot, volid, actualGiB, err
		}
		actualGiB = sizeGiB
	}
	return slot, volid, actualGiB, nil
}

// ReadDisk returns the volume id and size of the drive in a slot of a VM.
// An empty volume id means the slot is empty.
func (pc *ProxmoxClient) ReadDisk(ctx context.Context, node string, vmid int,
	slot string) (volid string, sizeGiB int, err error) {
	vm, err := pc.diskVM(ctx, node, vmid)
	if err != nil {
		return "", 0, err
	}
	value := driveValue(vm.VirtualMachineConfig, slot)
	if value == "" {
		return "", 0, nil
	}
	volid, sizeGiB = parseDriveValue(value)
	return volid, sizeGiB, nil
}

// ResizeDisk grows the drive in a slot to sizeGiB. Proxmox refuses to shrink.
func (pc *ProxmoxClient) ResizeDisk(ctx context.Context, node string, vmid int, slot string, sizeGiB int) error {
	vm, err := pc.diskVM(ctx, node, vmid)
	if err != nil {
		return err
	}
	task, err := vm.ResizeDisk(ctx, slot, fmt.Sprintf("%dG", sizeGiB))
	if err != nil {
		return fmt.Errorf("resizing %s on VM %d to %dG: %w", slot, vmid, sizeGiB, err)
	}
	if err = waitDiskTask(ctx, task, diskConfigTimeout); err != nil {
		return fmt.Errorf("resizing %s on VM %d to %dG: %w", slot, vmid, sizeGiB, err)
	}
	return nil
}

// DeleteHolderDisk frees the volume in a slot of the holder. Protection has
// to come off for that, since Proxmox refuses to remove a drive from a
// protected VM; it goes back on before returning, whatever happened.
func (pc *ProxmoxClient) DeleteHolderDisk(ctx context.Context, h *DiskHolder, slot string) (err error) {
	unlock := lockHolder(h.VMID)
	defer unlock()

	vm, err := pc.diskVM(ctx, h.Node, h.VMID)
	if err != nil {
		return err
	}
	value := driveValue(vm.VirtualMachineConfig, slot)
	if value == "" {
		return nil
	}
	volid, _ := parseDriveValue(value)

	if err = pc.setProtection(ctx, vm, false); err != nil {
		return err
	}
	defer func() {
		if perr := pc.setProtection(ctx, vm, true); perr != nil && err == nil {
			err = perr
		}
	}()

	// Removing the drive turns it into an unusedN entry; removing that entry
	// frees the volume.
	if err = pc.deleteConfigKey(ctx, vm, slot); err != nil {
		return err
	}
	vm, err = pc.diskVM(ctx, h.Node, h.VMID)
	if err != nil {
		return err
	}
	for key, unusedValue := range vm.VirtualMachineConfig.MergeUnuseds() {
		if unusedVolid, _ := parseDriveValue(unusedValue); unusedVolid == volid {
			return pc.deleteConfigKey(ctx, vm, key)
		}
	}
	return nil
}

// MoveDisk reassigns the drive in a slot of one VM to a slot of another VM
// on the same node and returns the volume id it has afterwards: Proxmox
// renames a volume after its new owner.
func (pc *ProxmoxClient) MoveDisk(ctx context.Context, node string, fromVMID int, slot string,
	toVMID int, targetSlot string) (volid string, err error) {
	vm, err := pc.diskVM(ctx, node, fromVMID)
	if err != nil {
		return "", err
	}
	task, err := vm.MoveDisk(ctx, slot, &proxmox.VirtualMachineMoveDiskOptions{
		TargetVMID: toVMID,
		TargetDisk: targetSlot,
	})
	if err != nil {
		return "", fmt.Errorf("moving %s from VM %d to VM %d %s: %w", slot, fromVMID, toVMID, targetSlot, err)
	}
	if err = waitDiskTask(ctx, task, diskConfigTimeout); err != nil {
		return "", fmt.Errorf("moving %s from VM %d to VM %d %s: %w", slot, fromVMID, toVMID, targetSlot, err)
	}
	volid, _, err = pc.ReadDisk(ctx, node, toVMID, targetSlot)
	if err != nil {
		return "", err
	}
	if volid == "" {
		return "", fmt.Errorf("moving %s from VM %d to VM %d: %s is empty afterwards", slot, fromVMID, toVMID, targetSlot)
	}
	return volid, nil
}

func (pc *ProxmoxClient) setProtection(ctx context.Context, vm *proxmox.VirtualMachine, on bool) error {
	value := 0
	if on {
		value = 1
	}
	task, err := vm.Config(ctx, proxmox.VirtualMachineOption{Name: "protection", Value: value})
	if err != nil {
		return fmt.Errorf("setting protection=%d on VM %d: %w", value, vm.VMID, err)
	}
	return waitDiskTask(ctx, task, diskConfigTimeout)
}

func (pc *ProxmoxClient) deleteConfigKey(ctx context.Context, vm *proxmox.VirtualMachine, key string) error {
	task, err := vm.Config(ctx, proxmox.VirtualMachineOption{Name: "delete", Value: key})
	if err != nil {
		return fmt.Errorf("removing %s from VM %d: %w", key, vm.VMID, err)
	}
	if err = waitDiskTask(ctx, task, diskConfigTimeout); err != nil {
		return fmt.Errorf("removing %s from VM %d: %w", key, vm.VMID, err)
	}
	return nil
}

// freeDiskSlot picks the first holder slot without a drive.
func freeDiskSlot(cfg *proxmox.VirtualMachineConfig) (string, error) {
	used := cfg.MergeDisks()
	for _, slot := range diskHolderSlots {
		if used[slot] == "" {
			return slot, nil
		}
	}
	return "", fmt.Errorf("no free drive slot left (%d in use)", len(used))
}

// driveValue returns the config value of a drive slot, or "" when empty.
func driveValue(cfg *proxmox.VirtualMachineConfig, slot string) string {
	if strings.HasPrefix(slot, "unused") {
		return cfg.MergeUnuseds()[slot]
	}
	return cfg.MergeDisks()[slot]
}

// parseDriveValue splits a drive config such as
// "main-pool:vm-9998-disk-0,size=10G,discard=on" into its volume id and its
// size in GiB. A size without a unit is in KiB, as qm reports it.
func parseDriveValue(value string) (volid string, sizeGiB int) {
	parts := strings.Split(value, ",")
	volid = parts[0]
	for _, p := range parts[1:] {
		if !strings.HasPrefix(p, "size=") {
			continue
		}
		sizeGiB = sizeToGiB(strings.TrimPrefix(p, "size="))
	}
	return volid, sizeGiB
}

// sizeToGiB converts a Proxmox size such as "10G", "512M", "1T" or a bare
// KiB count to whole GiB, rounding up so that a disk is never reported
// smaller than it is.
func sizeToGiB(s string) int {
	if s == "" {
		return 0
	}
	unit := s[len(s)-1]
	number := s
	factor := 1.0 / (1024 * 1024) // bare value: KiB
	switch unit {
	case 'K':
		number = s[:len(s)-1]
	case 'M':
		number = s[:len(s)-1]
		factor = 1.0 / 1024
	case 'G':
		number = s[:len(s)-1]
		factor = 1
	case 'T':
		number = s[:len(s)-1]
		factor = 1024
	case 'P':
		number = s[:len(s)-1]
		factor = 1024 * 1024
	}
	v, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0
	}
	return int(math.Ceil(v * factor))
}

// waitDiskTask waits for a Proxmox task and turns a failed one into a
// TaskError. A nil task is a config change Proxmox applied synchronously.
func waitDiskTask(ctx context.Context, task *proxmox.Task, timeout time.Duration) error {
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
		case <-time.After(diskTaskPollInterval):
		}
	}
}
