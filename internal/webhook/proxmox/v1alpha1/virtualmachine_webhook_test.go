package v1alpha1

import (
	"context"
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testVM(namespace, name, specName, conn string) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name:          specName,
			NodeName:      "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: conn},
		},
	}
}

func TestVirtualMachineValidator(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	existing := testVM("team-a", "web", "web", "pve")
	v := &VirtualMachineValidator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()}
	ctx := context.Background()

	// The ProxmoxConnection doesn't exist, so the Proxmox check only warns and
	// these cases exercise the cross-namespace check alone.
	onNode2 := testVM("team-b", "web", "web", "pve")
	onNode2.Spec.NodeName = "pve2"
	cases := []struct {
		name    string
		vm      *proxmoxv1alpha1.VirtualMachine
		wantErr bool
	}{
		{"same name in another namespace", testVM("team-b", "web", "web", "pve"), true},
		{"case-insensitive match", testVM("team-b", "other", "WEB", "pve"), true},
		{"same name on another connection", testVM("team-b", "web", "web", "pve-2"), false},
		{"same name on another node", onNode2, false},
		{"distinct name", testVM("team-b", "db", "db", "pve"), false},
		{"the existing object itself", existing, false},
	}
	for _, tc := range cases {
		_, err := v.ValidateCreate(ctx, tc.vm)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}

	// An update that keeps spec.name, the node and the connection is not re-checked.
	clash := testVM("team-b", "web", "web", "pve")
	if _, err := v.ValidateUpdate(ctx, clash, clash); err != nil {
		t.Errorf("unchanged update: unexpected error %v", err)
	}
	if _, err := v.ValidateUpdate(ctx, testVM("team-b", "web", "db", "pve"), clash); err == nil {
		t.Error("rename onto a taken name: expected error")
	}
	if _, err := v.ValidateUpdate(ctx, clash, onNode2); err != nil {
		t.Errorf("move off a taken node: unexpected error %v", err)
	}
	if _, err := v.ValidateUpdate(ctx, onNode2, clash); err == nil {
		t.Error("move onto a taken node: expected error")
	}
}
