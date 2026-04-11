package framework

import (
	"context"
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// E2EProxmoxConnectionName is the name of the ProxmoxConnection CR used in e2e tests.
	E2EProxmoxConnectionName = "e2e-proxmox"
)

// EnsureProxmoxConnection creates a ProxmoxConnection CR pointing to the containerized Proxmox.
// If the resource already exists, it is left as-is.
func (f *Framework) EnsureProxmoxConnection(ctx context.Context) error {
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: E2EProxmoxConnectionName,
		},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint:           f.ProxmoxEndpoint(),
			Username:           f.Config.Proxmox.Username,
			Password:           f.Config.Proxmox.Password,
			InsecureSkipVerify: f.Config.Proxmox.InsecureSkipVerify,
		},
	}

	err := f.KubeClient.Create(ctx, conn)
	if err != nil {
		if client.IgnoreAlreadyExists(err) == nil {
			return nil
		}
		return fmt.Errorf("creating ProxmoxConnection: %w", err)
	}
	return nil
}

// GetProxmoxConnection fetches the ProxmoxConnection CR by name.
func (f *Framework) GetProxmoxConnection(ctx context.Context, name string) (*proxmoxv1alpha1.ProxmoxConnection, error) {
	conn := &proxmoxv1alpha1.ProxmoxConnection{}
	err := f.KubeClient.Get(ctx, client.ObjectKey{Name: name}, conn)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// IsProxmoxConnectionReady checks if the ProxmoxConnection has a Ready condition set to True.
func IsProxmoxConnectionReady(conn *proxmoxv1alpha1.ProxmoxConnection) bool {
	return meta.IsStatusConditionTrue(conn.Status.Conditions, "Ready")
}

// DeleteProxmoxConnection deletes the e2e ProxmoxConnection CR.
func (f *Framework) DeleteProxmoxConnection(ctx context.Context) error {
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name: E2EProxmoxConnectionName,
		},
	}
	return client.IgnoreNotFound(f.KubeClient.Delete(ctx, conn))
}
