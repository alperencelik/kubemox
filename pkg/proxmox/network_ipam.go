package proxmox

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/luthermonson/go-proxmox"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// A network port of a machine is netN on Proxmox: "<model>=<mac>,bridge=<br>"
// with optional "tag=<vlan>" and other keys. The address a port gets from an
// IPAddressClaim goes into ipconfigN, which cloud-init reads from the
// cloud-init drive. The two are kept apart: the port keeps its MAC when its
// bridge or tag changes, and ipconfigN changes never rewrite the port.

const (
	ipconfigPrefix        = "ipconfig"
	networkConfigTimeout  = 5 * time.Minute
	networkPollInterval   = 2 * time.Second
	defaultNetworkModel   = "virtio"
	networkKeyBridge      = "bridge"
	networkKeyTag         = "tag"
	networkKeyMACFallback = "macaddr"
)

// NetworkIP is the address resolved for one port from its IPAddressClaim.
type NetworkIP struct {
	// Index is the N of netN and ipconfigN.
	Index int
	// Address is the address without prefix.
	Address string
	// Prefix is the prefix length.
	Prefix int
	// Gateway may be empty.
	Gateway string
}

// IPConfigValue renders the ipconfigN value Proxmox expects.
func (ip NetworkIP) IPConfigValue() string {
	v := fmt.Sprintf("ip=%s/%d", ip.Address, ip.Prefix)
	if ip.Gateway != "" {
		v += ",gw=" + ip.Gateway
	}
	return v
}

// parsedPort is a netN value taken apart.
type parsedPort struct {
	Model  string
	MAC    string
	Bridge string
	VLAN   int
	Extra  []string // other key=value pairs, kept verbatim
}

// parsePort takes a netN value such as "virtio=BC:24:11:0F:69:C2,bridge=vmbr0,tag=101,firewall=1" apart.
func parsePort(value string) parsedPort {
	p := parsedPort{}
	for i, part := range strings.Split(value, ",") {
		key, val, _ := strings.Cut(part, "=")
		switch {
		case i == 0 && key != networkKeyBridge && key != networkKeyTag:
			// The first pair is <model>=<mac>; an old-style value may instead
			// carry model=<model>,macaddr=<mac>.
			if key == "model" {
				p.Model = val
			} else {
				p.Model, p.MAC = key, val
			}
		case key == networkKeyBridge:
			p.Bridge = val
		case key == networkKeyTag:
			p.VLAN, _ = strconv.Atoi(val)
		case key == networkKeyMACFallback:
			p.MAC = val
		default:
			p.Extra = append(p.Extra, part)
		}
	}
	return p
}

// renderPort writes a netN value for the desired port, keeping the MAC and
// the extra keys of the current one so that a bridge or tag change does not
// hand the guest a new network card.
func renderPort(desired proxmoxv1alpha1.VirtualMachineNetwork, current *parsedPort) string {
	model := desired.Model
	if model == "" {
		model = defaultNetworkModel
	}
	first := model
	if current != nil && current.MAC != "" {
		first = model + "=" + current.MAC
	}
	parts := []string{first, networkKeyBridge + "=" + desired.Bridge}
	if desired.VLAN > 0 {
		parts = append(parts, fmt.Sprintf("%s=%d", networkKeyTag, desired.VLAN))
	}
	if current != nil {
		parts = append(parts, current.Extra...)
	}
	return strings.Join(parts, ",")
}

// portShape is what the spec and Proxmox are compared on: model, bridge and
// VLAN. The MAC is Proxmox's, the claim is the controller's.
type portShape struct {
	Model  string
	Bridge string
	VLAN   int
}

func shapeOfSpec(n proxmoxv1alpha1.VirtualMachineNetwork) portShape {
	model := n.Model
	if model == "" {
		model = defaultNetworkModel
	}
	return portShape{Model: model, Bridge: n.Bridge, VLAN: n.VLAN}
}

// networkShapes strips a network list down to what is compared with
// Proxmox: model, bridge and VLAN, with the model defaulted.
func networkShapes(networks []proxmoxv1alpha1.VirtualMachineNetwork) []proxmoxv1alpha1.VirtualMachineNetwork {
	if networks == nil {
		return nil
	}
	shapes := make([]proxmoxv1alpha1.VirtualMachineNetwork, 0, len(networks))
	for _, n := range networks {
		sh := shapeOfSpec(n)
		shapes = append(shapes, proxmoxv1alpha1.VirtualMachineNetwork{Model: sh.Model, Bridge: sh.Bridge, VLAN: sh.VLAN})
	}
	return shapes
}

// portIndex returns the N of a "netN" key, or -1.
func portIndex(key string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(key, netStr))
	if err != nil || !strings.HasPrefix(key, netStr) {
		return -1
	}
	return n
}

// ConfigureIPConfigs writes ipconfigN for every resolved address and clears
// the ipconfigN of ports that no longer have a claim, leaving everything else
// in the config alone. It returns whether anything changed.
func (pc *ProxmoxClient) ConfigureIPConfigs(vm *proxmoxv1alpha1.VirtualMachine, ips []NetworkIP) (bool, error) {
	virtualMachine, err := pc.getVirtualMachine(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return false, err
	}
	current := virtualMachine.VirtualMachineConfig.MergeIPConfigs()
	desired := map[string]string{}
	for _, ip := range ips {
		desired[fmt.Sprintf("%s%d", ipconfigPrefix, ip.Index)] = ip.IPConfigValue()
	}
	// Only the ports the spec gives a claim are the controller's; an ipconfig
	// written by hand on a port without a claim is left alone.
	claimed := map[string]bool{}
	for i, n := range getNetworks(vm) {
		if n.IPAddressClaimRef != nil {
			claimed[fmt.Sprintf("%s%d", ipconfigPrefix, i)] = true
		}
	}
	options := []proxmox.VirtualMachineOption{}
	toDelete := []string{}
	for key, value := range desired {
		if current[key] != value {
			options = append(options, proxmox.VirtualMachineOption{Name: key, Value: value})
		}
	}
	for key := range current {
		if claimed[key] && desired[key] == "" {
			toDelete = append(toDelete, key)
		}
	}
	if len(options) == 0 && len(toDelete) == 0 {
		return false, nil
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	sort.Strings(toDelete)
	if len(toDelete) > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "delete", Value: strings.Join(toDelete, ",")})
	}
	task, err := virtualMachine.Config(ctx, options...)
	if err != nil {
		return false, fmt.Errorf("writing ipconfig of VirtualMachine %s: %w", vm.Spec.Name, err)
	}
	if err = waitNetworkTask(ctx, task, networkConfigTimeout); err != nil {
		return false, fmt.Errorf("writing ipconfig of VirtualMachine %s: %w", vm.Spec.Name, err)
	}
	pc.dropCachedVM(vm.Spec.NodeName, vm.Spec.Name)
	return true, nil
}

// PortMACs returns the MAC of every netN of the machine, by index.
func (pc *ProxmoxClient) PortMACs(vm *proxmoxv1alpha1.VirtualMachine) (map[int]string, error) {
	nets, err := pc.GetNetworkConfiguration(vm)
	if err != nil {
		return nil, err
	}
	macs := map[int]string{}
	for key, value := range nets {
		if idx := portIndex(key); idx >= 0 {
			macs[idx] = parsePort(value).MAC
		}
	}
	return macs, nil
}

// dropCachedVM forgets the cached VM object so the next read sees the new config.
func (pc *ProxmoxClient) dropCachedVM(nodeName, vmName string) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	nc, ok := pc.nodesCache[nodeName]
	if !ok {
		return
	}
	if vmid, known := nc.vms[vmName]; known && nc.vmObjs != nil {
		delete(nc.vmObjs, vmid)
	}
}

// waitNetworkTask waits for a config task and turns a failed one into a
// TaskError. A nil task is a change Proxmox applied synchronously.
func waitNetworkTask(ctx context.Context, task *proxmox.Task, timeout time.Duration) error {
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
		case <-time.After(networkPollInterval):
		}
	}
}
