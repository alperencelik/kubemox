package proxmox

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	// typeNameConflictVirtualMachine is set when another VirtualMachine
	// already claims the same spec.name on the same node through the same
	// connection. Proxmox addresses a machine by name here, so two objects
	// with one name would both adopt one machine and reconcile it in turn.
	typeNameConflictVirtualMachine = "NameConflict"
	reasonNameConflict             = "NameConflict"
	reasonNameUnique               = "NameUnique"
)

// claimsSameMachine reports whether two VirtualMachine objects describe the
// same machine on Proxmox: same connection, same node, same spec.name. Objects
// in different namespaces are compared, which is the whole point: a namespace
// scopes the Kubernetes object, not the machine behind it.
func claimsSameMachine(a, b *proxmoxv1alpha1.VirtualMachine) bool {
	if a.UID == b.UID {
		return false
	}
	if a.Spec.Name != b.Spec.Name || a.Spec.NodeName != b.Spec.NodeName {
		return false
	}
	return connectionName(a) == connectionName(b)
}

func connectionName(vm *proxmoxv1alpha1.VirtualMachine) string {
	if vm.Spec.ConnectionRef == nil {
		return ""
	}
	return vm.Spec.ConnectionRef.Name
}

// nameOwner picks, among every object that claims the same machine as vm, the
// one that gets to own it: the oldest by creation time, by namespace/name when
// two were created in the same second. It returns nil when vm itself is that
// object, so a nil result means "proceed".
func nameOwner(vm *proxmoxv1alpha1.VirtualMachine, all []proxmoxv1alpha1.VirtualMachine) *proxmoxv1alpha1.VirtualMachine {
	claimants := []*proxmoxv1alpha1.VirtualMachine{vm}
	for i := range all {
		if claimsSameMachine(vm, &all[i]) {
			claimants = append(claimants, &all[i])
		}
	}
	if len(claimants) == 1 {
		return nil
	}
	sort.SliceStable(claimants, func(i, j int) bool {
		ti, tj := claimants[i].CreationTimestamp, claimants[j].CreationTimestamp
		if !ti.Equal(&tj) {
			return ti.Before(&tj)
		}
		ki := claimants[i].Namespace + "/" + claimants[i].Name
		kj := claimants[j].Namespace + "/" + claimants[j].Name
		return ki < kj
	})
	if claimants[0].UID == vm.UID {
		return nil
	}
	return claimants[0]
}

// checkNameConflict lists every VirtualMachine the manager watches and refuses
// to touch Proxmox for vm while an older object claims the same machine. It
// records the outcome as a condition either way, so a conflict that goes away
// (the other object deleted or renamed) is visible as NameConflict=False.
func (r *VirtualMachineReconciler) checkNameConflict(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (conflict bool, err error) {
	var list proxmoxv1alpha1.VirtualMachineList
	if err = r.List(ctx, &list); err != nil {
		return false, fmt.Errorf("listing VirtualMachines to check name uniqueness: %w", err)
	}
	owner := nameOwner(vm, list.Items)

	cond := metav1.Condition{
		Type:   typeNameConflictVirtualMachine,
		Status: metav1.ConditionFalse,
		Reason: reasonNameUnique,
		Message: fmt.Sprintf("no other VirtualMachine claims %q on node %q",
			vm.Spec.Name, vm.Spec.NodeName),
	}
	if owner != nil {
		cond.Status = metav1.ConditionTrue
		cond.Reason = reasonNameConflict
		cond.Message = fmt.Sprintf("VirtualMachine %s/%s already claims %q on node %q through connection %q; "+
			"this object is not reconciled until that one is deleted or renamed",
			owner.Namespace, owner.Name, vm.Spec.Name, vm.Spec.NodeName, connectionName(vm))
	}

	existing := meta.FindStatusCondition(vm.Status.Conditions, typeNameConflictVirtualMachine)
	if existing == nil || existing.Status != cond.Status || existing.Message != cond.Message {
		patch := client.MergeFrom(vm.DeepCopy())
		meta.SetStatusCondition(&vm.Status.Conditions, cond)
		if err = r.Status().Patch(ctx, vm, patch); err != nil {
			return owner != nil, client.IgnoreNotFound(err)
		}
		if owner != nil && r.Recorder != nil {
			r.Recorder.Eventf(vm, nil, "Warning", reasonNameConflict, "Skipped", "%s", cond.Message)
		}
	}
	return owner != nil, nil
}
