package proxmox

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func vmObj(ns, name, uid, spec, node, conn string, created time.Time) proxmoxv1alpha1.VirtualMachine {
	vm := proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         ns,
			Name:              name,
			UID:               types.UID(uid),
			CreationTimestamp: metav1.NewTime(created),
		},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{Name: spec, NodeName: node},
	}
	if conn != "" {
		vm.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: conn}
	}
	return vm
}

func TestNameOwner(t *testing.T) {
	t0 := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	older := vmObj("team-a", "web", "a", "web", "pve1", "prod", t0)
	newer := vmObj("team-b", "web", "b", "web", "pve1", "prod", t0.Add(time.Minute))
	otherNode := vmObj("team-b", "web", "c", "web", "pve2", "prod", t0)
	otherConn := vmObj("team-b", "web", "d", "web", "pve1", "lab", t0)
	otherName := vmObj("team-b", "db", "e", "db", "pve1", "prod", t0)
	sameSecondA := vmObj("team-a", "web", "f", "api", "pve1", "prod", t0)
	sameSecondB := vmObj("team-b", "web", "g", "api", "pve1", "prod", t0)

	all := []proxmoxv1alpha1.VirtualMachine{older, newer, otherNode, otherConn, otherName, sameSecondA, sameSecondB}

	cases := []struct {
		name string
		vm   proxmoxv1alpha1.VirtualMachine
		want string // namespace/name of the owner, "" when vm owns the machine
	}{
		{"oldest claimant owns", older, ""},
		{"newer claimant yields to the oldest", newer, "team-a/web"},
		{"same name on another node is a different machine", otherNode, ""},
		{"same name through another connection is a different machine", otherConn, ""},
		{"different name never conflicts", otherName, ""},
		{"same second: namespace/name order decides", sameSecondB, "team-a/web"},
		{"same second: the lower key owns", sameSecondA, ""},
		{"an object only conflicts with others, never with itself", vmObj("team-a", "web", "a", "web", "pve1", "prod", t0), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := tc.vm
			got := nameOwner(&vm, all)
			gotKey := ""
			if got != nil {
				gotKey = got.Namespace + "/" + got.Name
			}
			if gotKey != tc.want {
				t.Fatalf("nameOwner() = %q, want %q", gotKey, tc.want)
			}
		})
	}
}
