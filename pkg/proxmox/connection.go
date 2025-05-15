package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
)

type ProxmoxClient struct {
	Client *proxmox.Client
	// ctx    context.Context
}

func NewProxmoxClient(proxmoxConnection *proxmoxv1alpha1.ProxmoxConnection) *ProxmoxClient {
	// Create a new client
	proxmoxConfig := proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint:           fmt.Sprintf("https://%s:8006/api2/json", proxmoxConnection.Spec.Endpoint),
		Username:           proxmoxConnection.Spec.Username,
		Password:           proxmoxConnection.Spec.Password,
		TokenID:            proxmoxConnection.Spec.TokenID,
		Secret:             proxmoxConnection.Spec.Secret,
		InsecureSkipVerify: proxmoxConnection.Spec.InsecureSkipVerify,
	}
	var httpClient *http.Client
	if proxmoxConfig.InsecureSkipVerify {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint:gosec // Skipping linting for InsecureSkipVerify due to user choice
				},
			},
		}
	}
	var client *proxmox.Client
	switch {
	case proxmoxConfig.Username != "" && proxmoxConfig.Password != "":
		client = proxmox.NewClient(proxmoxConfig.Endpoint,
			proxmox.WithCredentials(&proxmox.Credentials{
				Username: proxmoxConfig.Username,
				Password: proxmoxConfig.Password,
			}),
			proxmox.WithHTTPClient(httpClient),
		)
	case proxmoxConfig.TokenID != "" && proxmoxConfig.Secret != "":
		client = proxmox.NewClient(proxmoxConfig.Endpoint,
			proxmox.WithAPIToken(proxmoxConfig.TokenID, proxmoxConfig.Secret),
			proxmox.WithHTTPClient(httpClient),
		)
	}

	return &ProxmoxClient{Client: client}
}

func (pc *ProxmoxClient) GetVersion() (*string, error) {
	version, err := pc.Client.Version(context.Background())
	if err != nil {
		return nil, err
	}
	return &version.Version, nil
}

func NewProxmoxClientFromRef(ctx context.Context, c cc.Client, ref *corev1.LocalObjectReference) (*ProxmoxClient, error) {
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("ProxmoxConnection reference is nil or empty")
	}
	conn := &proxmoxv1alpha1.ProxmoxConnection{}
	if err := c.Get(ctx, cc.ObjectKey{Name: ref.Name}, conn); err != nil {
		return nil, fmt.Errorf("getting ProxmoxConnection %q: %w", ref.Name, err)
	}
	return NewProxmoxClient(conn), nil
}
