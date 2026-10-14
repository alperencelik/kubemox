package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var virtualmachinelog = logf.Log.WithName("virtualmachine-resource")

// SetupVirtualMachineWebhookWithManager registers the webhook for VirtualMachine in the manager.
func SetupVirtualMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &proxmoxv1alpha1.VirtualMachine{}).
		WithValidator(&VirtualMachineValidator{Client: mgr.GetClient()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-proxmox-alperen-cloud-v1alpha1-virtualmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=proxmox.alperen.cloud,resources=virtualmachines,verbs=create;update,versions=v1alpha1,name=vvirtualmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// VirtualMachineValidator rejects a VirtualMachine whose spec.name would not
// identify exactly one Proxmox VM. VirtualMachines are namespaced but Proxmox
// names are not, so two namespaces must not claim the same name on the same
// ProxmoxConnection.
type VirtualMachineValidator struct {
	Client client.Client
}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type VirtualMachine.
func (v *VirtualMachineValidator) ValidateCreate(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (admission.Warnings, error) {
	virtualmachinelog.V(1).Info("Validation for VirtualMachine upon creation", "namespace", vm.Namespace, "name", vm.Name)
	return v.validateName(ctx, vm)
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type VirtualMachine.
func (v *VirtualMachineValidator) ValidateUpdate(ctx context.Context,
	oldVM, vm *proxmoxv1alpha1.VirtualMachine) (admission.Warnings, error) {
	virtualmachinelog.V(1).Info("Validation for VirtualMachine upon update", "namespace", vm.Namespace, "name", vm.Name)
	if strings.EqualFold(oldVM.Spec.Name, vm.Spec.Name) && oldVM.Spec.NodeName == vm.Spec.NodeName &&
		connectionName(oldVM) == connectionName(vm) {
		return nil, nil
	}
	return v.validateName(ctx, vm)
}

// ValidateDelete implements admission.Validator; deletes are not validated.
func (v *VirtualMachineValidator) ValidateDelete(context.Context,
	*proxmoxv1alpha1.VirtualMachine) (admission.Warnings, error) {
	return nil, nil
}

// ponytail: list-then-admit, so two concurrent creates of the same name can
// both pass, and both would then manage (and delete) the same Proxmox VM. Add
// an atomic claim, e.g. a Lease per connection/node/name, if that race shows
// up in practice.
func (v *VirtualMachineValidator) validateName(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (admission.Warnings, error) {
	vms := &proxmoxv1alpha1.VirtualMachineList{}
	if err := v.Client.List(ctx, vms); err != nil {
		return nil, fmt.Errorf("listing VirtualMachines: %w", err)
	}
	for i := range vms.Items {
		other := &vms.Items[i]
		if other.Namespace == vm.Namespace && other.Name == vm.Name {
			continue
		}
		// Names are matched case-insensitively, like the controller does on Proxmox.
		if connectionName(other) == connectionName(vm) && other.Spec.NodeName == vm.Spec.NodeName &&
			strings.EqualFold(other.Spec.Name, vm.Spec.Name) {
			return nil, fmt.Errorf("spec.name %q is already used by VirtualMachine %s/%s on node %q of ProxmoxConnection %q",
				vm.Spec.Name, other.Namespace, other.Name, vm.Spec.NodeName, connectionName(vm))
		}
	}

	// Adopting a single existing VM is fine; only a name Proxmox can't resolve
	// to one VM is rejected. An unreachable Proxmox must not block applies,
	// the controller reports that on its own.
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, v.Client, vm.Spec.ConnectionRef)
	if err != nil {
		return admission.Warnings{fmt.Sprintf("could not check spec.name on Proxmox: %v", err)}, nil
	}
	ids, err := pc.VMIDsByName(ctx, vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return admission.Warnings{fmt.Sprintf("could not check spec.name on Proxmox: %v", err)}, nil
	}
	if len(ids) > 1 {
		return nil, fmt.Errorf("spec.name %q matches %d virtual machines on Proxmox node %q (VMIDs %v); rename them so the name is unique",
			vm.Spec.Name, len(ids), vm.Spec.NodeName, ids)
	}
	return nil, nil
}

func connectionName(vm *proxmoxv1alpha1.VirtualMachine) string {
	if vm.Spec.ConnectionRef == nil {
		return ""
	}
	return vm.Spec.ConnectionRef.Name
}
