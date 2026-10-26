package proxmox

import (
	"strings"
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	testVztmpl  = "local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst"
	testRootfs  = "local-lvm"
	testSSHKey  = "ssh-ed25519 AAAAC3Nza user@host"
	testNet0Key = "net0"
	testDNS     = "198.18.0.53"
)

func vlan(v int32) *int32 { return &v }

// tenantNIC is a NIC as a tenant gets one: its own VLAN and a static address.
func tenantNIC() proxmoxv1alpha1.ContainerTemplateNetwork {
	return proxmoxv1alpha1.ContainerTemplateNetwork{
		Bridge: "vmbr1", VLAN: vlan(201), IP: "198.18.1.23/24", Gateway: "198.18.1.1", MTU: 1500, Firewall: true,
	}
}

func TestContainerNetValue(t *testing.T) {
	cases := []struct {
		nic  proxmoxv1alpha1.ContainerTemplateNetwork
		want string
	}{
		{proxmoxv1alpha1.ContainerTemplateNetwork{}, "name=eth1,bridge=vmbr0,ip=dhcp"},
		{tenantNIC(), "name=eth1,bridge=vmbr1,tag=201,ip=198.18.1.23/24,gw=198.18.1.1,mtu=1500,firewall=1"},
		{proxmoxv1alpha1.ContainerTemplateNetwork{Bridge: "vmbr2", IP: "manual"}, "name=eth1,bridge=vmbr2,ip=manual"},
	}
	for _, tc := range cases {
		if got := containerNetValue(1, tc.nic); got != tc.want {
			t.Errorf("containerNetValue(%+v) = %q, want %q", tc.nic, got, tc.want)
		}
	}
}

func TestCreateFromOSTemplate(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{})
	obj := testContainer(uidA, 0)
	obj.Spec.Template = proxmoxv1alpha1.ContainerTemplate{
		OSTemplate: testVztmpl, Unprivileged: true, Features: "nesting=1",
		SSHPublicKeys: []string{testSSHKey, "ssh-ed25519 AAAAC3Nzb other@host"},
		Nameserver:    testDNS, SearchDomain: "tenant-a.internal",
		Cores: 1, Memory: 1024,
		Disk:    []proxmoxv1alpha1.ContainerTemplateDisk{{Storage: testRootfs, Size: 8}},
		Network: []proxmoxv1alpha1.ContainerTemplateNetwork{tenantNIC(), {Bridge: "vmbr0"}},
	}

	id, err := pc.CloneContainer(obj)
	if err != nil {
		t.Fatal(err)
	}
	ct := f.cts[id]
	want := map[string]string{
		"ostemplate":      testVztmpl,
		"rootfs":          testRootfs + ":8",
		testNet0Key:       containerNetValue(0, tenantNIC()),
		"net1":            "name=eth1,bridge=vmbr0,ip=dhcp",
		"unprivileged":    "1",
		"features":        "nesting=1",
		"ssh-public-keys": testSSHKey + "\nssh-ed25519 AAAAC3Nzb other@host",
		"nameserver":      testDNS,
		"searchdomain":    "tenant-a.internal",
	}
	for k, v := range want {
		if ct.config[k] != v {
			t.Errorf("%s = %q, want %q", k, ct.config[k], v)
		}
	}
	if ct.name != testCTName || ct.tags != "kubemox-uid-"+uidA {
		t.Errorf("hostname %q, tags %q", ct.name, ct.tags)
	}
}

// The OCI path used to render net0 itself, always with ip=dhcp.
func TestOCICreateUsesTheSpecNICs(t *testing.T) {
	obj := testContainer(uidA, 0)
	obj.Spec.Template.Image = &proxmoxv1alpha1.ContainerTemplateImage{Reference: "nginx:1.27", Storage: "local"}
	obj.Spec.Template.Disk = []proxmoxv1alpha1.ContainerTemplateDisk{{Storage: testRootfs, Size: 4}}
	obj.Spec.Template.Network = []proxmoxv1alpha1.ContainerTemplateNetwork{tenantNIC()}

	opts, err := newContainerOptions(obj, "local:vztmpl/nginx-1.27.tar", testCTName)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.Name == testNet0Key && o.Value != containerNetValue(0, tenantNIC()) {
			t.Fatalf("net0 = %v", o.Value)
		}
	}
}

func TestCloneGetsDiskSizeAndNICs(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{9000: {
		name: testTemplate, status: VirtualMachineStoppedState,
		config: map[string]string{
			"rootfs":    testRootfs + ":subvol-9000-disk-0,size=4G",
			testNet0Key: "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth",
		},
	}})
	obj := testContainer(uidA, 0)
	obj.Spec.Template = proxmoxv1alpha1.ContainerTemplate{
		Name:       testTemplate,
		Nameserver: testDNS,
		Disk:       []proxmoxv1alpha1.ContainerTemplateDisk{{Storage: testRootfs, Size: 8}},
		Network:    []proxmoxv1alpha1.ContainerTemplateNetwork{tenantNIC()},
	}

	id, err := pc.CloneContainer(obj)
	if err != nil {
		t.Fatal(err)
	}
	cfg := f.cts[id].config
	if cfg["rootfs"] != testRootfs+":subvol-9000-disk-0,size=8G" {
		t.Errorf("rootfs = %q, want it grown to 8G", cfg["rootfs"])
	}
	if cfg[testNet0Key] != containerNetValue(0, tenantNIC()) || cfg["nameserver"] != testDNS {
		t.Errorf("net0 = %q, nameserver = %q", cfg[testNet0Key], cfg["nameserver"])
	}

	// A disk size the template already has is not a resize: Proxmox cannot shrink.
	obj.Spec.Template.Disk[0].Size = 2
	before := len(f.calls)
	if _, err := pc.CloneContainer(obj); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls[before:] {
		if strings.HasSuffix(c, "/resize") {
			t.Fatalf("CloneContainer called %s for a smaller disk", c)
		}
	}
}

func TestUpdateContainerChangesNICsAndKeepsTheMAC(t *testing.T) {
	f, pc := newFakeLXCHost(t, map[int]*fakeCT{100: {
		name: testCTName, status: VirtualMachineRunningState, tags: "kubemox-uid-" + uidA,
		config: map[string]string{testNet0Key: "name=eth0,bridge=vmbr1,hwaddr=BC:24:11:00:00:01,ip=dhcp,type=veth"},
	}})
	obj := testContainer(uidA, 100)
	obj.Spec.Template.Network = []proxmoxv1alpha1.ContainerTemplateNetwork{tenantNIC()}

	if err := pc.UpdateContainer(obj); err != nil {
		t.Fatal(err)
	}
	got := f.cts[100].config[testNet0Key]
	if got != containerNetValue(0, tenantNIC())+",hwaddr=BC:24:11:00:00:01" {
		t.Fatalf("net0 = %q", got)
	}

	// Proxmox adds type=veth and may order the keys its own way: no change.
	f.cts[100].config[testNet0Key] = "name=eth0,bridge=vmbr1,firewall=1,gw=198.18.1.1,hwaddr=BC:24:11:00:00:01," +
		"ip=198.18.1.23/24,mtu=1500,tag=201,type=veth"
	before := len(f.calls)
	if err := pc.UpdateContainer(obj); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls[before:] {
		if strings.HasPrefix(c, "PUT ") {
			t.Fatalf("UpdateContainer wrote %s although nothing changed", c)
		}
	}
}

func TestRootfsSizeGiB(t *testing.T) {
	cases := map[string]float64{
		"local-zfs:subvol-101-disk-0,size=8G":   8,
		"local:101/vm-101-disk-0.raw,size=512M": 0.5,
		"local-lvm:vm-101-disk-0,size=1T":       1024,
		"local-lvm:vm-101-disk-0":               0,
	}
	for in, want := range cases {
		if got := rootfsSizeGiB(in); got != want {
			t.Errorf("rootfsSizeGiB(%q) = %v, want %v", in, got, want)
		}
	}
}
