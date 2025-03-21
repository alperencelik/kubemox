package proxmox

import (
	"context"
	"fmt"
	"net/http"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/luthermonson/go-proxmox"
)

type ProxmoxInstance struct {
	Client *proxmox.Client
	ctx    context.Context
}

func NewProxmoxClient(proxmoxConnection proxmoxv1alpha1.ProxmoxConnection) ProxmoxInstance {
	// Create a new client
	proxmoxConfig := proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: fmt.Sprintf("https://%s:8006/api2/json", proxmoxConnection.Spec.Endpoint),
		Username: proxmoxConnection.Spec.Username,
		Password: proxmoxConnection.Spec.Password,
		TokenID:  proxmoxConnection.Spec.TokenID,
		Secret:   proxmoxConnection.Spec.Secret,
	}
	var httpClient *http.Client
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
	return ProxmoxInstance{Client: client}
}

func (pi *ProxmoxInstance) GetVersion() (*string, error) {
	version, err := pi.Client.Version(context.Background())
	if err != nil {
		return nil, err
	}
	return &version.Version, nil

}
