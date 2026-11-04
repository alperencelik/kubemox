package v1alpha1

import (
	"context"
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/guestname"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var namelog = logf.Log.WithName("name-webhook")

// +kubebuilder:webhook:path=/validate-proxmox-alperen-cloud-v1alpha1-virtualmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=proxmox.alperen.cloud,resources=virtualmachines,verbs=create;update,versions=v1alpha1,name=vvirtualmachine-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-proxmox-alperen-cloud-v1alpha1-virtualmachinetemplate,mutating=false,failurePolicy=fail,sideEffects=None,groups=proxmox.alperen.cloud,resources=virtualmachinetemplates,verbs=create;update,versions=v1alpha1,name=vvirtualmachinetemplate-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-proxmox-alperen-cloud-v1alpha1-container,mutating=false,failurePolicy=fail,sideEffects=None,groups=proxmox.alperen.cloud,resources=containers,verbs=create;update,versions=v1alpha1,name=vcontainer-v1alpha1.kb.io,admissionReviewVersions=v1

// SetupNameWebhooksWithManager registers the spec.name validators for
// VirtualMachine, VirtualMachineTemplate and Container.
func SetupNameWebhooksWithManager(mgr ctrl.Manager) error {
	c := mgr.GetClient()
	if err := ctrl.NewWebhookManagedBy(mgr, &proxmoxv1alpha1.VirtualMachine{}).
		WithValidator(&nameValidator[*proxmoxv1alpha1.VirtualMachine]{client: c, claim: guestname.ForVM}).
		Complete(); err != nil {
		return err
	}
	if err := ctrl.NewWebhookManagedBy(mgr, &proxmoxv1alpha1.VirtualMachineTemplate{}).
		WithValidator(&nameValidator[*proxmoxv1alpha1.VirtualMachineTemplate]{client: c, claim: guestname.ForTemplate}).
		Complete(); err != nil {
		return err
	}
	return ctrl.NewWebhookManagedBy(mgr, &proxmoxv1alpha1.Container{}).
		WithValidator(&nameValidator[*proxmoxv1alpha1.Container]{client: c, claim: guestname.ForContainer}).
		Complete()
}

// nameValidator rejects a resource whose spec.name would not identify exactly
// one Proxmox guest. The resources are namespaced but Proxmox names are not, so
// two namespaces must not claim the same name on the same ProxmoxConnection.
type nameValidator[T runtime.Object] struct {
	client client.Client
	claim  func(T) guestname.Claim
}

func (v *nameValidator[T]) ValidateCreate(ctx context.Context, obj T) (admission.Warnings, error) {
	return v.validate(ctx, v.claim(obj))
}

func (v *nameValidator[T]) ValidateUpdate(ctx context.Context, oldObj, obj T) (admission.Warnings, error) {
	c := v.claim(obj)
	if c.SameGuest(v.claim(oldObj)) {
		return nil, nil
	}
	return v.validate(ctx, c)
}

func (v *nameValidator[T]) ValidateDelete(context.Context, T) (admission.Warnings, error) {
	return nil, nil
}

// List-then-admit, so two concurrent creates of the same name can both pass;
// the controllers then let the older one own the guest (see guestname.Owner).
func (v *nameValidator[T]) validate(ctx context.Context, c guestname.Claim) (admission.Warnings, error) {
	namelog.V(1).Info("validating spec.name", "resource", c.String())
	others, err := guestname.List(ctx, v.client, c.LXC)
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		if !o.SameObject(c) && c.SameGuest(o) {
			return nil, fmt.Errorf("spec.name %q is already used by %s on node %q of ProxmoxConnection %q",
				c.Guest, o, c.Node, c.Conn())
		}
	}

	// Adopting a single existing guest is fine; only a name Proxmox can't
	// resolve to one guest is rejected. An unreachable Proxmox must not block
	// applies, the controller reports that on its own.
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, v.client, c.ConnRef)
	if err != nil {
		return admission.Warnings{fmt.Sprintf("could not check spec.name on Proxmox: %v", err)}, nil
	}
	lookup := pc.VMIDsByName
	if c.LXC {
		lookup = pc.ContainerIDsByName
	}
	ids, err := lookup(ctx, c.Guest, c.Node)
	if err != nil {
		return admission.Warnings{fmt.Sprintf("could not check spec.name on Proxmox: %v", err)}, nil
	}
	if len(ids) > 1 {
		return nil, fmt.Errorf("spec.name %q matches %d guests on Proxmox node %q (VMIDs %v); rename them so the name is unique",
			c.Guest, len(ids), c.Node, ids)
	}
	return nil, nil
}
