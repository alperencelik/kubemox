// Package proxmoxconn bridges the aggregated apiserver to Kubemox's
// ProxmoxConnection / VirtualMachine CRs. Every registry handler asks the
// Resolver to translate a kubemox VM name (or list of names) into a live
// Proxmox client plus the node + VM-name needed for the upstream API call.
//
// The Resolver intentionally has no Proxmox knowledge of its own; it
// delegates client construction to pkg/proxmox.NewProxmoxClientFromRef,
// which already caches clients keyed by ProxmoxConnection resourceVersion.
package proxmoxconn

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pveops "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// Resolver looks up kubemox CRs and produces Proxmox clients.
type Resolver struct {
	// Client is a controller-runtime client with a watch-backed cache over
	// VirtualMachine, ProxmoxConnection (and any Secret indirection added
	// in future). Use NewCachedClient to build one with the right schemes.
	Client client.Client
}

// New returns a Resolver wrapping the given client. The client must have
// the kubemox proxmox/v1alpha1 scheme registered.
func New(c client.Client) *Resolver {
	return &Resolver{Client: c}
}

// VMTarget describes a fully-resolved kubemox VM: the Proxmox client to
// use, the node it lives on, and the Proxmox VM name (which equals the
// CR name in kubemox today).
type VMTarget struct {
	CR       *proxmoxv1alpha1.VirtualMachine
	Client   *proxmox.ProxmoxClient
	NodeName string
	VMName   string
}

// ResolveVM looks up a kubemox VirtualMachine CR by name (cluster-scoped)
// and returns the Proxmox client + node + name needed to call the upstream
// API. Returns an apierrors-typed NotFound error if the CR is missing.
func (r *Resolver) ResolveVM(ctx context.Context, name string) (*VMTarget, error) {
	cr := &proxmoxv1alpha1.VirtualMachine{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, cr); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(pveops.Resource("virtualmachines"), name)
		}
		return nil, fmt.Errorf("get VirtualMachine %q: %w", name, err)
	}

	if cr.Spec.ConnectionRef == nil || cr.Spec.ConnectionRef.Name == "" {
		return nil, fmt.Errorf("VirtualMachine %q has no connectionRef", name)
	}

	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client,
		&corev1.LocalObjectReference{Name: cr.Spec.ConnectionRef.Name})
	if err != nil {
		return nil, fmt.Errorf("build proxmox client for VM %q: %w", name, err)
	}

	return &VMTarget{
		CR:       cr,
		Client:   pc,
		NodeName: cr.Spec.NodeName,
		VMName:   cr.Spec.Name,
	}, nil
}

// ListVMs returns every kubemox VirtualMachine CR. The aggregated VM list
// projection is one entry per CR; live status is filled in by the caller.
func (r *Resolver) ListVMs(ctx context.Context) ([]proxmoxv1alpha1.VirtualMachine, error) {
	list := &proxmoxv1alpha1.VirtualMachineList{}
	if err := r.Client.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list VirtualMachines: %w", err)
	}
	return list.Items, nil
}

// ListConnections returns every ProxmoxConnection CR. Used by the node
// projection to enumerate all reachable Proxmox clusters.
func (r *Resolver) ListConnections(ctx context.Context) ([]proxmoxv1alpha1.ProxmoxConnection, error) {
	list := &proxmoxv1alpha1.ProxmoxConnectionList{}
	if err := r.Client.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list ProxmoxConnections: %w", err)
	}
	return list.Items, nil
}

// ClientFor returns a cached Proxmox client for a named connection. Used
// by the node and task registries which key off the connection name
// directly rather than a VM name.
func (r *Resolver) ClientFor(ctx context.Context, connectionName string) (*proxmox.ProxmoxClient, error) {
	return proxmox.NewProxmoxClientFromRef(ctx, r.Client,
		&corev1.LocalObjectReference{Name: connectionName})
}

