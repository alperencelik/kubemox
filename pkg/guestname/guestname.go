// Package guestname decides which Kubemox resource owns a Proxmox guest name.
//
// VirtualMachines, VirtualMachineTemplates and Containers are namespaced, but
// the guests they manage are found on Proxmox by name, and Proxmox names are
// not namespaced. When several resources claim the same name, the oldest one
// owns the guest and the others must leave it alone. The validating webhook
// rejects most such claims up front; this is what holds when two creates race
// past it or the webhook is disabled.
package guestname

import (
	"context"
	"fmt"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConditionNameConflict is set to True on a resource whose spec.name is owned
// by another resource. Such a resource is not reconciled against Proxmox.
const ConditionNameConflict = "NameConflict"

// Claim is the Proxmox guest a resource manages: spec.name on a node of a
// ProxmoxConnection. VirtualMachines and VirtualMachineTemplates are both QEMU
// guests that the controllers resolve through the same case-insensitive name
// lookup, so they share one space. Containers are looked up on their own, by
// exact name.
type Claim struct {
	Kind, Namespace, Name string // the claiming resource
	Created               metav1.Time
	LXC                   bool
	ConnRef               *corev1.LocalObjectReference
	Node, Guest           string
}

// ForVM returns the claim of a VirtualMachine.
func ForVM(vm *proxmoxv1alpha1.VirtualMachine) Claim {
	return Claim{Kind: "VirtualMachine", Namespace: vm.Namespace, Name: vm.Name, Created: vm.CreationTimestamp,
		ConnRef: vm.Spec.ConnectionRef, Node: vm.Spec.NodeName, Guest: vm.Spec.Name}
}

// ForTemplate returns the claim of a VirtualMachineTemplate.
func ForTemplate(t *proxmoxv1alpha1.VirtualMachineTemplate) Claim {
	return Claim{Kind: "VirtualMachineTemplate", Namespace: t.Namespace, Name: t.Name, Created: t.CreationTimestamp,
		ConnRef: t.Spec.ConnectionRef, Node: t.Spec.NodeName, Guest: t.Spec.Name}
}

// ForContainer returns the claim of a Container.
func ForContainer(ct *proxmoxv1alpha1.Container) Claim {
	return Claim{Kind: "Container", Namespace: ct.Namespace, Name: ct.Name, Created: ct.CreationTimestamp, LXC: true,
		ConnRef: ct.Spec.ConnectionRef, Node: ct.Spec.NodeName, Guest: ct.Spec.Name}
}

// Conn returns the name of the claim's ProxmoxConnection.
func (c Claim) Conn() string {
	if c.ConnRef == nil {
		return ""
	}
	return c.ConnRef.Name
}

// SameGuest reports whether c and o name the same Proxmox guest.
func (c Claim) SameGuest(o Claim) bool {
	if c.LXC != o.LXC || c.Conn() != o.Conn() || c.Node != o.Node {
		return false
	}
	if c.LXC {
		return c.Guest == o.Guest
	}
	return strings.EqualFold(c.Guest, o.Guest)
}

// SameObject reports whether c and o were made from the same resource.
func (c Claim) SameObject(o Claim) bool {
	return c.Kind == o.Kind && c.Namespace == o.Namespace && c.Name == o.Name
}

// String names the claiming resource, e.g. "VirtualMachine team-a/web".
func (c Claim) String() string {
	return fmt.Sprintf("%s %s/%s", c.Kind, c.Namespace, c.Name)
}

// before orders claims oldest first; ties are broken by kind, namespace and
// name so every reconcile picks the same owner.
func (c Claim) before(o Claim) bool {
	if !c.Created.Equal(&o.Created) {
		return c.Created.Before(&o.Created)
	}
	if c.Kind != o.Kind {
		return c.Kind < o.Kind
	}
	if c.Namespace != o.Namespace {
		return c.Namespace < o.Namespace
	}
	return c.Name < o.Name
}

// List returns the claims of every resource in the QEMU space, or in the
// container space if lxc is set.
func List(ctx context.Context, r client.Reader, lxc bool) ([]Claim, error) {
	var claims []Claim
	if lxc {
		cts := &proxmoxv1alpha1.ContainerList{}
		if err := r.List(ctx, cts); err != nil {
			return nil, fmt.Errorf("listing Containers: %w", err)
		}
		for i := range cts.Items {
			claims = append(claims, ForContainer(&cts.Items[i]))
		}
		return claims, nil
	}
	vms := &proxmoxv1alpha1.VirtualMachineList{}
	if err := r.List(ctx, vms); err != nil {
		return nil, fmt.Errorf("listing VirtualMachines: %w", err)
	}
	for i := range vms.Items {
		claims = append(claims, ForVM(&vms.Items[i]))
	}
	templates := &proxmoxv1alpha1.VirtualMachineTemplateList{}
	if err := r.List(ctx, templates); err != nil {
		return nil, fmt.Errorf("listing VirtualMachineTemplates: %w", err)
	}
	for i := range templates.Items {
		claims = append(claims, ForTemplate(&templates.Items[i]))
	}
	return claims, nil
}

// Owner returns the claim that owns c's guest: the oldest resource claiming
// the same name, which may be c itself. Resources being deleted still count,
// so a guest isn't handed over while its owner's finalizer is deleting it.
//
// ponytail: VirtualMachines and VirtualMachineTemplates come from two informer
// caches, so a VM and a template created within the cache lag of each other
// can both see themselves as owner. Same-kind claims are ordered by the watch.
func Owner(ctx context.Context, r client.Reader, c Claim) (Claim, error) {
	claims, err := List(ctx, r, c.LXC)
	if err != nil {
		return Claim{}, err
	}
	owner := c
	for _, o := range claims {
		if c.SameGuest(o) && o.before(owner) {
			owner = o
		}
	}
	return owner, nil
}
