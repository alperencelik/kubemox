package proxmox

import (
	"context"
	"testing"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/guestname"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func claimTestVM(ns string, created time.Time) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "web", CreationTimestamp: metav1.NewTime(created),
			Finalizers: []string{virtualMachineFinalizerName}},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{Name: "web", NodeName: "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: "pve"}},
	}
}

func TestClaimGuest(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	owner := claimTestVM("team-a", now)
	owner.Status.Conditions = []metav1.Condition{{Type: guestname.ConditionNameConflict, Status: metav1.ConditionTrue,
		Reason: "NameTaken", LastTransitionTime: metav1.NewTime(now)}}
	loser := claimTestVM("team-b", now.Add(time.Second))
	deleting := claimTestVM("team-c", now.Add(2*time.Second))
	deleting.DeletionTimestamp = &metav1.Time{Time: now}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(owner, loser, deleting).
		WithStatusSubresource(&proxmoxv1alpha1.VirtualMachine{}).Build()
	ctx := context.Background()

	get := func(vm *proxmoxv1alpha1.VirtualMachine) *proxmoxv1alpha1.VirtualMachine {
		t.Helper()
		got := &proxmoxv1alpha1.VirtualMachine{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(vm), got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	claim := func(vm *proxmoxv1alpha1.VirtualMachine) (bool, time.Duration) {
		t.Helper()
		vm = get(vm)
		owned, res, err := claimGuest(ctx, c, vm, guestname.ForVM(vm), virtualMachineFinalizerName)
		if err != nil {
			t.Fatal(err)
		}
		return owned, res.RequeueAfter
	}

	// The oldest claim owns the guest, and a stale NameConflict is cleared.
	if owned, _ := claim(owner); !owned {
		t.Error("oldest VirtualMachine: not owned")
	}
	if meta.FindStatusCondition(get(owner).Status.Conditions, guestname.ConditionNameConflict) != nil {
		t.Error("oldest VirtualMachine: NameConflict not cleared")
	}

	// A newer claim is marked and polls for the name to be freed.
	if owned, requeue := claim(loser); owned || requeue != nameConflictRequeue {
		t.Errorf("newer VirtualMachine: owned=%v requeue=%v", owned, requeue)
	}
	if !meta.IsStatusConditionTrue(get(loser).Status.Conditions, guestname.ConditionNameConflict) {
		t.Error("newer VirtualMachine: NameConflict not set")
	}

	// A newer claim being deleted lets go without touching the owner's guest.
	if owned, requeue := claim(deleting); owned || requeue != 0 {
		t.Errorf("deleting VirtualMachine: owned=%v requeue=%v", owned, requeue)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(deleting), &proxmoxv1alpha1.VirtualMachine{}); err == nil {
		t.Error("deleting VirtualMachine: finalizer kept, object still present")
	}

	// Once the owner is gone, the next oldest takes over.
	if err := c.Delete(ctx, get(owner)); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, func() *proxmoxv1alpha1.VirtualMachine {
		vm := get(owner)
		vm.Finalizers = nil
		return vm
	}()); err != nil {
		t.Fatal(err)
	}
	if owned, _ := claim(loser); !owned {
		t.Error("newer VirtualMachine after the owner is gone: not owned")
	}
	if meta.FindStatusCondition(get(loser).Status.Conditions, guestname.ConditionNameConflict) != nil {
		t.Error("newer VirtualMachine after the owner is gone: NameConflict not cleared")
	}
}
