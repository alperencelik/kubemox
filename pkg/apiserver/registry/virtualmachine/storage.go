// Package virtualmachine implements the rest.Storage interfaces for the
// /apis/ops.proxmox.alperen.cloud/v1alpha1/virtualmachines resource and
// its subresources. The VirtualMachine resource is a live projection of
// kubemox VirtualMachine CRs — list/get only; writes go to the CR.
package virtualmachine

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
)

// Storage serves list/get for VirtualMachine.
type Storage struct {
	resolver *proxmoxconn.Resolver
}

// NewStorage constructs the read storage.
func NewStorage(r *proxmoxconn.Resolver) *Storage {
	return &Storage{resolver: r}
}

var (
	_ rest.Storage              = (*Storage)(nil)
	_ rest.Scoper               = (*Storage)(nil)
	_ rest.Lister               = (*Storage)(nil)
	_ rest.Getter               = (*Storage)(nil)
	_ rest.SingularNameProvider = (*Storage)(nil)
	_ rest.TableConvertor       = (*Storage)(nil)
)

func (s *Storage) New() runtime.Object     { return &pveopsv1alpha1.VirtualMachine{} }
func (s *Storage) NewList() runtime.Object { return &pveopsv1alpha1.VirtualMachineList{} }
func (s *Storage) Destroy()                {}
func (s *Storage) NamespaceScoped() bool   { return false }
func (s *Storage) GetSingularName() string { return "virtualmachine" }

// Get returns the live projection for a single VM.
func (s *Storage) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	target, err := s.resolver.ResolveVM(ctx, name)
	if err != nil {
		return nil, err
	}
	st, err := target.Client.UpdateVMStatus(target.VMName, target.NodeName)
	out := &pveopsv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:              target.CR.Name,
			UID:               target.CR.UID,
			ResourceVersion:   target.CR.ResourceVersion,
			CreationTimestamp: target.CR.CreationTimestamp,
		},
		Spec: pveopsv1alpha1.VirtualMachineSpec{
			NodeName:       target.NodeName,
			ConnectionName: target.CR.Spec.ConnectionRef.Name,
		},
	}
	if err == nil && st != nil {
		out.Spec.VMID = st.ID
		out.Status = pveopsv1alpha1.VirtualMachineStatus{
			State:     st.State,
			IPAddress: st.IPAddress,
			OSInfo:    st.OSInfo,
		}
	}
	return out, nil
}

// List returns one projection per kubemox VirtualMachine CR.
func (s *Storage) List(ctx context.Context, _ *internalversion.ListOptions) (runtime.Object, error) {
	crs, err := s.resolver.ListVMs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	out := &pveopsv1alpha1.VirtualMachineList{Items: make([]pveopsv1alpha1.VirtualMachine, 0, len(crs))}
	for i := range crs {
		cr := &crs[i]
		item := pveopsv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:              cr.Name,
				UID:               cr.UID,
				ResourceVersion:   cr.ResourceVersion,
				CreationTimestamp: cr.CreationTimestamp,
			},
			Spec: pveopsv1alpha1.VirtualMachineSpec{NodeName: cr.Spec.NodeName},
		}
		if cr.Spec.ConnectionRef != nil {
			item.Spec.ConnectionName = cr.Spec.ConnectionRef.Name
		}
		if cr.Status.Status != nil {
			item.Status = pveopsv1alpha1.VirtualMachineStatus{
				State:     cr.Status.Status.State,
				IPAddress: cr.Status.Status.IPAddress,
				OSInfo:    cr.Status.Status.OSInfo,
			}
			item.Spec.VMID = cr.Status.Status.ID
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// ConvertToTable provides the default columns shown by kubectl.
func (s *Storage) ConvertToTable(ctx context.Context, obj runtime.Object,
	_ runtime.Object) (*metav1.Table, error) {
	t := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Node", Type: "string"},
			{Name: "State", Type: "string"},
			{Name: "IP", Type: "string"},
			{Name: "Connection", Type: "string"},
		},
	}
	add := func(vm *pveopsv1alpha1.VirtualMachine) {
		t.Rows = append(t.Rows, metav1.TableRow{
			Cells:  []interface{}{vm.Name, vm.Spec.NodeName, vm.Status.State, vm.Status.IPAddress, vm.Spec.ConnectionName},
			Object: runtime.RawExtension{Object: vm},
		})
	}
	switch v := obj.(type) {
	case *pveopsv1alpha1.VirtualMachine:
		add(v)
	case *pveopsv1alpha1.VirtualMachineList:
		for i := range v.Items {
			add(&v.Items[i])
		}
	}
	return t, nil
}
