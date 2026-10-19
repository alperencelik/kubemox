package proxmox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// A NoCloud datasource is an ISO labelled "cidata" with user-data, meta-data
// and optionally network-config and vendor-data. Proxmox's own cloud-init
// drive (ide2: <storage>:cloudinit) only carries what qm's ci* options can
// express; arbitrary user-data needs a cicustom snippet, and the Proxmox API
// has no call that writes a snippet. The ISO goes through the upload API
// instead: go-proxmox builds it, uploads it to the node's ISO storage as
// user-data-<vmid>.iso, attaches it at the device, and removes it again
// with the machine.

// NoCloudData is the content of the datasource.
type NoCloudData struct {
	UserData      string
	MetaData      string
	VendorData    string
	NetworkConfig string
}

// Hash identifies the content, so that an unchanged datasource is not
// rebuilt and a changed one gets a new instance identity.
func (d NoCloudData) Hash() string {
	h := sha256.New()
	for _, part := range []string{d.UserData, d.MetaData, d.VendorData, d.NetworkConfig} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PortAddress is what the generated network-config needs about one port.
type PortAddress struct {
	MACAddress string
	// Address with prefix, such as 10.0.0.5/24.
	Address string
	Gateway string
}

// RenderMetaData writes the meta-data of a datasource: the instance id
// cloud-init compares with the one it ran for last, and the hostname.
func RenderMetaData(instanceID, hostname string) string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, hostname)
}

// RenderNetworkConfig writes a netplan (network-config version 2) that
// gives each port with an address a static configuration, matched by MAC
// so that the guest's interface naming does not matter, and leaves the
// other ports to DHCP. Empty when no port has an address.
func RenderNetworkConfig(ports []PortAddress, nameservers []string) string {
	addressed := 0
	for _, p := range ports {
		if p.Address != "" && p.MACAddress != "" {
			addressed++
		}
	}
	if addressed == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("version: 2\nethernets:\n")
	for i, p := range ports {
		if p.MACAddress == "" {
			continue
		}
		fmt.Fprintf(&b, "  eth%d:\n    match:\n      macaddress: %s\n    set-name: eth%d\n",
			i, strings.ToLower(p.MACAddress), i)
		if p.Address == "" {
			b.WriteString("    dhcp4: true\n")
			continue
		}
		fmt.Fprintf(&b, "    dhcp4: false\n    addresses:\n      - %s\n", p.Address)
		if p.Gateway != "" {
			fmt.Fprintf(&b, "    routes:\n      - to: default\n        via: %s\n", p.Gateway)
		}
		if len(nameservers) > 0 {
			sorted := append([]string(nil), nameservers...)
			sort.Strings(sorted)
			b.WriteString("    nameservers:\n      addresses:\n")
			for _, ns := range sorted {
				fmt.Fprintf(&b, "        - %s\n", ns)
			}
		}
	}
	return b.String()
}

// ApplyNoCloud builds the datasource ISO, uploads it and attaches it at the
// device. An ISO already attached by an earlier apply is unmounted and
// removed first, since the storage keeps one file per machine.
func (pc *ProxmoxClient) ApplyNoCloud(vm *proxmoxv1alpha1.VirtualMachine, device string, data NoCloudData) error {
	virtualMachine, err := pc.getVirtualMachine(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	if virtualMachine.HasTag(proxmox.MakeTag(proxmox.TagCloudInit)) {
		if err = virtualMachine.UnmountCloudInitISO(ctx, device); err != nil {
			return fmt.Errorf("removing the previous cloud-init ISO of VirtualMachine %s: %w", vm.Spec.Name, err)
		}
	}
	// The library appends the ISO device to the machine's boot order as
	// "<boot>;<device>". A machine built from scratch has no boot order yet,
	// and ";ide2" is not one Proxmox accepts, so one is written first: the
	// first disk, which is also what the guest should boot from.
	if err = pc.ensureBootOrder(virtualMachine); err != nil {
		return err
	}
	err = virtualMachine.CloudInit(ctx, device, data.UserData, data.MetaData, data.VendorData, data.NetworkConfig)
	if err != nil {
		return fmt.Errorf("attaching the cloud-init ISO to VirtualMachine %s: %w", vm.Spec.Name, err)
	}
	pc.dropCachedVM(vm.Spec.NodeName, vm.Spec.Name)
	return nil
}

// ensureBootOrder writes "boot: order=<first disk>" on a machine that has
// no boot order, and refreshes the config so the caller sees it.
func (pc *ProxmoxClient) ensureBootOrder(virtualMachine *proxmox.VirtualMachine) error {
	cfg := virtualMachine.VirtualMachineConfig
	if cfg == nil || cfg.Boot != "" {
		return nil
	}
	first := firstBootDisk(cfg.MergeDisks())
	if first == "" {
		return nil
	}
	task, err := virtualMachine.Config(ctx, proxmox.VirtualMachineOption{Name: "boot", Value: "order=" + first})
	if err != nil {
		return fmt.Errorf("setting the boot order of VM %d: %w", virtualMachine.VMID, err)
	}
	if err = waitNetworkTask(ctx, task, networkConfigTimeout); err != nil {
		return fmt.Errorf("setting the boot order of VM %d: %w", virtualMachine.VMID, err)
	}
	cfg.Boot = "order=" + first
	return nil
}

// firstBootDisk picks the drive to boot from among the machine's drives:
// the lowest slot that is not a CD-ROM, scsi before virtio before sata
// before ide.
func firstBootDisk(drives map[string]string) string {
	for _, prefix := range []string{"scsi", "virtio", "sata", "ide"} {
		best := ""
		bestIndex := -1
		for slot, value := range drives {
			if !strings.HasPrefix(slot, prefix) || strings.Contains(value, "media=cdrom") {
				continue
			}
			idx, err := strconv.Atoi(strings.TrimPrefix(slot, prefix))
			if err != nil {
				continue
			}
			if bestIndex < 0 || idx < bestIndex {
				best, bestIndex = slot, idx
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}
