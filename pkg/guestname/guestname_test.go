package guestname

import (
	"context"
	"testing"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var t0 = metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

func vm(ns, guest string, created metav1.Time) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web", CreationTimestamp: created},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{Name: guest, NodeName: "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"}},
	}
}

func reader(t *testing.T, objs ...client.Object) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func TestOwnerIsOldestClaim(t *testing.T) {
	older := vm("team-b", "web", t0)
	newer := vm("team-a", "WEB", metav1.NewTime(t0.Add(time.Second)))
	tmpl := &proxmoxv1alpha1.VirtualMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-c", Name: "t",
			CreationTimestamp: metav1.NewTime(t0.Add(-time.Second))},
		Spec: proxmoxv1alpha1.VirtualMachineTemplateSpec{Name: "other", NodeName: "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"}},
	}
	r := reader(t, older, newer, tmpl)
	ctx := context.Background()

	for _, c := range []Claim{ForVM(older), ForVM(newer)} {
		owner, err := Owner(ctx, r, c)
		if err != nil {
			t.Fatal(err)
		}
		if !owner.SameObject(ForVM(older)) {
			t.Errorf("owner of %s = %s, want %s", c, owner, ForVM(older))
		}
	}

	// A template with the same name is older still, so it wins over both VMs.
	tmpl.Spec.Name = "Web"
	r = reader(t, older, newer, tmpl)
	if owner, _ := Owner(ctx, r, ForVM(older)); !owner.SameObject(ForTemplate(tmpl)) {
		t.Errorf("owner = %s, want the older template", owner)
	}
}

func TestOwnerTieBreak(t *testing.T) {
	a := vm("team-a", "web", t0)
	b := vm("team-b", "web", t0)
	r := reader(t, a, b)
	for _, c := range []Claim{ForVM(a), ForVM(b)} {
		owner, err := Owner(context.Background(), r, c)
		if err != nil {
			t.Fatal(err)
		}
		if !owner.SameObject(ForVM(a)) {
			t.Errorf("owner of %s = %s, want %s", c, owner, ForVM(a))
		}
	}
}
