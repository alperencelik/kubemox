package proxmox

import (
	"fmt"
	"maps"
	"strconv"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmox "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Container config keys for DNS.
const (
	containerOptionNameserver   = "nameserver"
	containerOptionSearchDomain = "searchdomain"
)

// containerNetValue renders spec NIC i as a Proxmox netN value.
func containerNetValue(i int, n proxmoxv1alpha1.ContainerTemplateNetwork) string {
	bridge := n.Bridge
	if bridge == "" {
		bridge = "vmbr0"
	}
	ip := n.IP
	if ip == "" {
		ip = "dhcp"
	}
	v := fmt.Sprintf("name=eth%d,bridge=%s", i, bridge)
	if n.VLAN != nil {
		v += fmt.Sprintf(",tag=%d", *n.VLAN)
	}
	v += ",ip=" + ip
	if n.Gateway != "" {
		v += ",gw=" + n.Gateway
	}
	if n.MTU > 0 {
		v += fmt.Sprintf(",mtu=%d", n.MTU)
	}
	if n.Firewall {
		v += ",firewall=1"
	}
	return v
}

// containerNetOptions are the netN options of the spec. Without NICs in the
// spec the container gets eth0 on vmbr0 with DHCP.
func containerNetOptions(ct *proxmoxv1alpha1.Container) []proxmox.ContainerOption {
	nets := ct.Spec.Template.Network
	if len(nets) == 0 {
		nets = []proxmoxv1alpha1.ContainerTemplateNetwork{{}}
	}
	opts := make([]proxmox.ContainerOption, 0, len(nets))
	for i, n := range nets {
		opts = append(opts, proxmox.ContainerOption{Name: fmt.Sprintf("net%d", i), Value: containerNetValue(i, n)})
	}
	return opts
}

// containerGuestOptions are the guest settings that are plain config keys, so a
// clone can get them after the fact as well.
func containerGuestOptions(ct *proxmoxv1alpha1.Container) []proxmox.ContainerOption {
	t := ct.Spec.Template
	var opts []proxmox.ContainerOption
	if t.Nameserver != "" {
		opts = append(opts, proxmox.ContainerOption{Name: containerOptionNameserver, Value: t.Nameserver})
	}
	if t.SearchDomain != "" {
		opts = append(opts, proxmox.ContainerOption{Name: containerOptionSearchDomain, Value: t.SearchDomain})
	}
	if t.Features != "" {
		opts = append(opts, proxmox.ContainerOption{Name: "features", Value: t.Features})
	}
	return opts
}

// containerCreateOnlyOptions exist only as parameters of a create.
func containerCreateOnlyOptions(ct *proxmoxv1alpha1.Container) []proxmox.ContainerOption {
	t := ct.Spec.Template
	var opts []proxmox.ContainerOption
	if t.Unprivileged {
		opts = append(opts, proxmox.ContainerOption{Name: "unprivileged", Value: 1})
	}
	if len(t.SSHPublicKeys) > 0 {
		opts = append(opts, proxmox.ContainerOption{Name: "ssh-public-keys", Value: strings.Join(t.SSHPublicKeys, "\n")})
	}
	return opts
}

// netKV splits a netN value into its keys. firewall=0 is the same as no
// firewall key.
func netKV(v string) map[string]string {
	kv := map[string]string{}
	for _, part := range strings.Split(v, ",") {
		if k, val, ok := strings.Cut(part, "="); ok && (k != "firewall" || val != "0") {
			kv[k] = val
		}
	}
	return kv
}

// sameNIC compares what the spec decides of a NIC. hwaddr and type are
// Proxmox's own keys.
func sameNIC(want, have map[string]string) bool {
	delete(have, "hwaddr")
	delete(have, "type")
	return maps.Equal(want, have)
}

// netChanges returns the netN options whose managed keys differ from what
// the container has. Each keeps the NIC's hwaddr, so its MAC stays the same.
func netChanges(ct *proxmoxv1alpha1.Container, have map[string]string) []proxmox.ContainerOption {
	var changes []proxmox.ContainerOption
	for i, n := range ct.Spec.Template.Network {
		key := fmt.Sprintf("net%d", i)
		got := netKV(have[key])
		hw := got["hwaddr"]
		if sameNIC(netKV(containerNetValue(i, n)), got) {
			continue
		}
		v := containerNetValue(i, n)
		if hw != "" {
			v += ",hwaddr=" + hw
		}
		changes = append(changes, proxmox.ContainerOption{Name: key, Value: v})
	}
	return changes
}

// rootfsSizeGiB reads size=<n><unit> from a rootfs value such as
// "local-zfs:subvol-101-disk-0,size=8G".
func rootfsSizeGiB(rootfs string) float64 {
	units := map[byte]float64{'K': 1.0 / (1 << 20), 'M': 1.0 / (1 << 10), 'G': 1, 'T': 1 << 10}
	for _, part := range strings.Split(rootfs, ",") {
		v, ok := strings.CutPrefix(part, "size=")
		if !ok || v == "" {
			continue
		}
		if unit, ok := units[v[len(v)-1]]; ok {
			n, _ := strconv.ParseFloat(v[:len(v)-1], 64)
			return n * unit
		}
		n, _ := strconv.ParseFloat(v, 64)
		return n / (1 << 30)
	}
	return 0
}

// applyCloneSettings gives a fresh clone what a create would have set: the
// rootfs grown to template.disk[0].size, the NICs, DNS and features.
func (pc *ProxmoxClient) applyCloneSettings(clone *proxmox.Container, ct *proxmoxv1alpha1.Container) error {
	disks := ct.Spec.Template.Disk
	if len(disks) > 0 && float64(disks[0].Size) > rootfsSizeGiB(clone.ContainerConfig.RootFS) {
		task, err := clone.Resize(ctx, "rootfs", fmt.Sprintf("%dG", disks[0].Size))
		if err != nil {
			return fmt.Errorf("growing the rootfs of container %s: %w", ct.Spec.Name, err)
		}
		if ok, done, err := task.WaitForCompleteStatus(ctx, 60, 5); !done || !ok {
			return fmt.Errorf("growing the rootfs of container %s: %v %w", ct.Spec.Name, task.ExitStatus, err)
		}
	}
	opts := append(containerNetOptions(ct), containerGuestOptions(ct)...)
	if _, err := clone.Config(ctx, opts...); err != nil {
		return fmt.Errorf("configuring container %s: %w", ct.Spec.Name, err)
	}
	return nil
}

// updateContainerNets applies changed NICs to the container, running or not:
// Proxmox hot-plugs a container's network.
func updateContainerNets(c *proxmox.Container, ct *proxmoxv1alpha1.Container) error {
	if c.ContainerConfig == nil {
		return nil
	}
	changes := netChanges(ct, c.ContainerConfig.Nets)
	if len(changes) == 0 {
		return nil
	}
	if _, err := c.Config(ctx, changes...); err != nil {
		return fmt.Errorf("updating the NICs of container %s: %w", ct.Spec.Name, err)
	}
	log.Log.Info(fmt.Sprintf("Container %s NICs have been updated", ct.Spec.Name))
	return nil
}
