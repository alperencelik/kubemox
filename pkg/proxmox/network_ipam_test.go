package proxmox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	testBridge  = "vmbr70"
	testBridge0 = "vmbr0"
	testMAC     = "BC:24:11:0F:69:C2"
)

func TestParseAndRenderPort(t *testing.T) {
	p := parsePort("virtio=" + testMAC + ",bridge=" + testBridge + ",tag=101,firewall=1")
	if p.Model != defaultNetworkModel || p.MAC != testMAC || p.Bridge != testBridge || p.VLAN != 101 ||
		len(p.Extra) != 1 || p.Extra[0] != "firewall=1" {
		t.Fatalf("parsePort = %+v", p)
	}
	legacy := parsePort("model=e1000,macaddr=" + testMAC + ",bridge=" + testBridge0)
	if legacy.Model != "e1000" || legacy.MAC != testMAC || legacy.Bridge != testBridge0 || legacy.VLAN != 0 {
		t.Fatalf("legacy parsePort = %+v", legacy)
	}

	desired := proxmoxv1alpha1.VirtualMachineNetwork{Model: defaultNetworkModel, Bridge: testBridge, VLAN: 102}
	// A new port has no MAC: Proxmox picks one.
	if got := renderPort(desired, nil); got != "virtio,bridge=vmbr70,tag=102" {
		t.Fatalf("renderPort(new) = %q", got)
	}
	// An existing port keeps its MAC and its extra keys across a tag change.
	if got := renderPort(desired, &p); got != "virtio="+testMAC+",bridge=vmbr70,tag=102,firewall=1" {
		t.Fatalf("renderPort(existing) = %q", got)
	}
	// Dropping the VLAN drops the tag.
	desired.VLAN = 0
	if got := renderPort(desired, &p); got != "virtio="+testMAC+",bridge=vmbr70,firewall=1" {
		t.Fatalf("renderPort(untagged) = %q", got)
	}
}

func TestParseNetworkConfigurationIsOrderedAndIgnoresClaims(t *testing.T) {
	nets, err := parseNetworkConfiguration(map[string]string{
		"net2": "virtio=" + testMAC + ",bridge=vmbr2",
		"net0": "virtio=" + testMAC + ",bridge=vmbr0,tag=5",
		"net1": "e1000=" + testMAC + ",bridge=vmbr1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 || nets[0].Bridge != testBridge0 || nets[0].VLAN != 5 ||
		nets[1].Model != "e1000" || nets[2].Bridge != "vmbr2" {
		t.Fatalf("parseNetworkConfiguration = %+v", nets)
	}
	if _, err = parseNetworkConfiguration(map[string]string{"net0": "virtio"}); err == nil {
		t.Fatal("a port without a bridge parsed")
	}

	spec := []proxmoxv1alpha1.VirtualMachineNetwork{
		{
			Model: defaultNetworkModel, Bridge: testBridge0, VLAN: 5,
			IPAddressClaimRef: &corev1.LocalObjectReference{Name: "web-0"},
		},
	}
	actual := []proxmoxv1alpha1.VirtualMachineNetwork{{Model: defaultNetworkModel, Bridge: testBridge0, VLAN: 5}}
	if networkShapes(spec)[0] != networkShapes(actual)[0] {
		t.Fatal("a claim ref made the port shapes differ")
	}
	untagged := proxmoxv1alpha1.VirtualMachineNetwork{Model: defaultNetworkModel, Bridge: testBridge0}
	if shapeOfSpec(spec[0]) == shapeOfSpec(untagged) {
		t.Fatal("a VLAN change was not seen")
	}
}

func TestIPConfigValue(t *testing.T) {
	full := NetworkIP{Index: 0, Address: "10.0.0.5", Prefix: 24, Gateway: "10.0.0.1"}
	if got := full.IPConfigValue(); got != "ip=10.0.0.5/24,gw=10.0.0.1" {
		t.Fatalf("IPConfigValue = %q", got)
	}
	if got := (NetworkIP{Index: 1, Address: "10.0.1.5", Prefix: 16}).IPConfigValue(); got != "ip=10.0.1.5/16" {
		t.Fatalf("IPConfigValue without gateway = %q", got)
	}
	if portIndex("net12") != 12 || portIndex("ipconfig0") != -1 || portIndex("netx") != -1 {
		t.Fatal("portIndex")
	}
}
