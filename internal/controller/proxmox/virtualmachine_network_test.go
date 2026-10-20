package proxmox

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	testModel  = "virtio"
	testBridge = "vmbr70"
)

func claimObj(ns, name, addressName string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(ipAddressClaimGVK)
	u.SetNamespace(ns)
	u.SetName(name)
	if addressName != "" {
		_ = unstructured.SetNestedField(u.Object, addressName, "status", "addressRef", "name")
	}
	return u
}

func addressObj(ns, name, addr string, prefix int64, gw string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(ipAddressGVK)
	u.SetNamespace(ns)
	u.SetName(name)
	_ = unstructured.SetNestedField(u.Object, addr, "spec", "address")
	_ = unstructured.SetNestedField(u.Object, prefix, "spec", "prefix")
	if gw != "" {
		_ = unstructured.SetNestedField(u.Object, gw, "spec", "gateway")
	}
	return u
}

func TestResolveNetworkIPs(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = proxmoxv1alpha1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		claimObj("team-a", "web-0", "web-0-addr"),
		addressObj("team-a", "web-0-addr", "10.208.64.10", 24, "10.208.64.1"),
		claimObj("team-a", "web-1", ""),
	).Build()
	r := &VirtualMachineReconciler{Client: c}

	vm := &proxmoxv1alpha1.VirtualMachine{}
	vm.Namespace = "team-a"
	vm.Spec.VMSpec = &proxmoxv1alpha1.NewVMSpec{Network: &[]proxmoxv1alpha1.VirtualMachineNetwork{
		{Model: testModel, Bridge: testBridge, VLAN: 101, IPAddressClaimRef: &corev1.LocalObjectReference{Name: "web-0"}},
		{Model: testModel, Bridge: "vmbr0"},
		{Model: testModel, Bridge: testBridge, VLAN: 102, IPAddressClaimRef: &corev1.LocalObjectReference{Name: "web-1"}},
		{Model: testModel, Bridge: testBridge, VLAN: 103, IPAddressClaimRef: &corev1.LocalObjectReference{Name: "missing"}},
	}}
	ips, waiting, err := r.resolveNetworkIPs(context.Background(), vm)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].Index != 0 || ips[0].Address != "10.208.64.10" || ips[0].Prefix != 24 || ips[0].Gateway != "10.208.64.1" {
		t.Fatalf("ips = %+v", ips)
	}
	if len(waiting) != 2 || waiting[0] != "web-1" || !strings.HasPrefix(waiting[1], "missing") {
		t.Fatalf("waiting = %v", waiting)
	}

	// An IPAddress without a prefix is a broken object, not a wait.
	broken := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(
		claimObj("team-a", "web-0", "web-0-addr"),
		addressObj("team-a", "web-0-addr", "10.208.64.10", 0, ""),
	).Build()
	if _, _, err = (&VirtualMachineReconciler{Client: broken}).resolveNetworkIPs(context.Background(), vm); err == nil ||
		!strings.Contains(err.Error(), "prefix") {
		t.Fatalf("broken address err = %v", err)
	}
}
