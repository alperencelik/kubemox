package v1alpha1

import (
	"context"
	"testing"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/guestname"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testNode           = "pve1"
	caseOtherNamespace = "same name in another namespace"
	caseItself         = "the existing object itself"
)

func testVM(namespace, name, specName, conn string) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name:          specName,
			NodeName:      testNode,
			ConnectionRef: &corev1.LocalObjectReference{Name: conn},
		},
	}
}

func testTemplate(namespace, name, specName string) *proxmoxv1alpha1.VirtualMachineTemplate {
	return &proxmoxv1alpha1.VirtualMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: proxmoxv1alpha1.VirtualMachineTemplateSpec{
			Name:          specName,
			NodeName:      testNode,
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"},
		},
	}
}

func testContainer(namespace, name, specName string) *proxmoxv1alpha1.Container {
	return &proxmoxv1alpha1.Container{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: proxmoxv1alpha1.ContainerSpec{
			Name:          specName,
			NodeName:      testNode,
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"},
		},
	}
}

func testClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// The ProxmoxConnection doesn't exist in these tests, so the Proxmox check only
// warns and they exercise the cross-namespace check alone.

func TestVirtualMachineNames(t *testing.T) {
	existing := testVM("team-a", "web", "web", "pve")
	c := testClient(t, existing, testTemplate("team-a", "ubuntu", "ubuntu-tmpl"), testContainer("team-a", "db", "db"))
	v := &nameValidator[*proxmoxv1alpha1.VirtualMachine]{client: c, claim: guestname.ForVM}
	ctx := context.Background()

	onNode2 := testVM("team-b", "web", "web", "pve")
	onNode2.Spec.NodeName = "pve2"
	cases := []struct {
		name    string
		vm      *proxmoxv1alpha1.VirtualMachine
		wantErr bool
	}{
		{caseOtherNamespace, testVM("team-b", "web", "web", "pve"), true},
		{"case-insensitive match", testVM("team-b", "other", "WEB", "pve"), true},
		{"name of a VirtualMachineTemplate", testVM("team-b", "x", "Ubuntu-Tmpl", "pve"), true},
		{"name of a Container", testVM("team-b", "db", "db", "pve"), false},
		{"same name on another connection", testVM("team-b", "web", "web", "pve-2"), false},
		{"same name on another node", onNode2, false},
		{"distinct name", testVM("team-b", "api", "api", "pve"), false},
		{caseItself, existing, false},
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
	if _, err := v.ValidateUpdate(ctx, testVM("team-b", "web", "api", "pve"), clash); err == nil {
		t.Error("rename onto a taken name: expected error")
	}
	if _, err := v.ValidateUpdate(ctx, clash, onNode2); err != nil {
		t.Errorf("move off a taken node: unexpected error %v", err)
	}
	if _, err := v.ValidateUpdate(ctx, onNode2, clash); err == nil {
		t.Error("move onto a taken node: expected error")
	}
}

func TestVirtualMachineTemplateNames(t *testing.T) {
	existing := testTemplate("team-a", "ubuntu", "ubuntu")
	c := testClient(t, existing, testVM("team-a", "web", "web", "pve"))
	v := &nameValidator[*proxmoxv1alpha1.VirtualMachineTemplate]{client: c, claim: guestname.ForTemplate}
	ctx := context.Background()

	cases := []struct {
		name    string
		tmpl    *proxmoxv1alpha1.VirtualMachineTemplate
		wantErr bool
	}{
		{caseOtherNamespace, testTemplate("team-b", "ubuntu", "ubuntu"), true},
		{"name of a VirtualMachine", testTemplate("team-b", "x", "WEB"), true},
		{"distinct name", testTemplate("team-b", "debian", "debian"), false},
		{caseItself, existing, false},
	}
	for _, tc := range cases {
		_, err := v.ValidateCreate(ctx, tc.tmpl)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestContainerNames(t *testing.T) {
	existing := testContainer("team-a", "db", "db")
	c := testClient(t, existing, testVM("team-a", "web", "web", "pve"))
	v := &nameValidator[*proxmoxv1alpha1.Container]{client: c, claim: guestname.ForContainer}
	ctx := context.Background()

	cases := []struct {
		name    string
		ct      *proxmoxv1alpha1.Container
		wantErr bool
	}{
		{caseOtherNamespace, testContainer("team-b", "db", "db"), true},
		// The container controller matches names exactly.
		{"different case", testContainer("team-b", "db", "DB"), false},
		{"name of a VirtualMachine", testContainer("team-b", "web", "web"), false},
		{caseItself, existing, false},
	}
	for _, tc := range cases {
		_, err := v.ValidateCreate(ctx, tc.ct)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
