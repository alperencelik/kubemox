package proxmox

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestBuildNoCloudData(t *testing.T) {
	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Spec.Name = "web"
	vm.Spec.VMSpec = &proxmoxv1alpha1.NewVMSpec{CloudInit: &proxmoxv1alpha1.CloudInitSource{
		SecretRef: corev1.LocalObjectReference{Name: "web-ci"}, Nameservers: []string{"10.0.0.1"},
	}}
	vm.Status.Networks = []proxmoxv1alpha1.VirtualMachineNetworkStatus{
		{Index: 0, MACAddress: "BC:24:11:00:00:01", Address: "10.0.0.5/24", Gateway: "10.0.0.1"},
	}
	secret := &corev1.Secret{Data: map[string][]byte{"user-data": []byte("#cloud-config\nhostname: web\n")}}

	data, instanceID, err := buildNoCloudData(vm, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(instanceID, "web-") || !strings.Contains(data.MetaData, "instance-id: "+instanceID) ||
		!strings.Contains(data.MetaData, "local-hostname: web") {
		t.Fatalf("generated meta-data = %q (instance %q)", data.MetaData, instanceID)
	}
	if !strings.Contains(data.NetworkConfig, "10.0.0.5/24") || !strings.Contains(data.NetworkConfig, "- 10.0.0.1") {
		t.Fatalf("generated network-config = %q", data.NetworkConfig)
	}

	// A content change gives a new instance id; the Secret's own meta-data wins.
	secret.Data["user-data"] = []byte("#cloud-config\nhostname: web2\n")
	_, instanceID2, _ := buildNoCloudData(vm, secret)
	if instanceID2 == instanceID {
		t.Fatal("changed user-data kept the instance id")
	}
	secret.Data["meta-data"] = []byte("instance-id: fixed-1\n")
	secret.Data["network-config"] = []byte("version: 2\n")
	data, instanceID3, _ := buildNoCloudData(vm, secret)
	if instanceID3 != "fixed-1" || data.NetworkConfig != "version: 2\n" {
		t.Fatalf("Secret-provided meta-data/network-config not used: %q %q", instanceID3, data.NetworkConfig)
	}

	// No user-data is a broken Secret.
	if _, _, err = buildNoCloudData(vm, &corev1.Secret{Data: map[string][]byte{"meta-data": []byte("x")}}); err == nil {
		t.Fatal("a Secret without user-data was accepted")
	}
}
