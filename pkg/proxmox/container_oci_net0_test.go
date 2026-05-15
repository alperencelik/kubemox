package proxmox

import (
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestLxcNet0Equivalent_orderInsensitive(t *testing.T) {
	a := "name=eth0,bridge=vmbr0,ip=dhcp,tag=42"
	b := "bridge=vmbr0,ip=dhcp,name=eth0,tag=42"
	if !lxcNet0Equivalent(a, b) {
		t.Fatalf("expected equivalent net0 strings")
	}
}

func TestNet0FromSpec_VLAN(t *testing.T) {
	v := int32(100)
	ct := &proxmoxv1alpha1.Container{
		Spec: proxmoxv1alpha1.ContainerSpec{
			Template: proxmoxv1alpha1.ContainerTemplate{
				Network: []proxmoxv1alpha1.ContainerTemplateNetwork{
					{Bridge: "vmbr1", VLAN: &v},
				},
			},
		},
	}
	got := net0FromSpec(ct)
	want := "name=eth0,bridge=vmbr1,ip=dhcp,tag=100"
	if got != want {
		t.Fatalf("net0FromSpec: got %q want %q", got, want)
	}
}

func TestNet0IdentityCanonical_ignoresIPAndExtraKeys(t *testing.T) {
	a := "name=eth0,bridge=vmbr0,ip=dhcp,firewall=1"
	b := "bridge=vmbr0,name=eth0,ip=10.0.0.5/24,hwaddr=aa:bb:cc:dd:ee:ff"
	if Net0IdentityCanonical(a) != Net0IdentityCanonical(b) {
		t.Fatalf("identity: %q vs %q", Net0IdentityCanonical(a), Net0IdentityCanonical(b))
	}
}

func TestContainerNet0MatchesSpec_proxmoxDHCPExpansion(t *testing.T) {
	ct := &proxmoxv1alpha1.Container{
		Spec: proxmoxv1alpha1.ContainerSpec{
			Template: proxmoxv1alpha1.ContainerTemplate{},
		},
	}
	got := "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=192.168.1.10/24,gw=192.168.1.1"
	if !ContainerNet0MatchesSpec(got, ct) {
		t.Fatal("expected default spec to match Proxmox net0 after DHCP")
	}
}
