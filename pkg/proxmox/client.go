package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	proxmox "github.com/luthermonson/go-proxmox"
)

var (
	// Create Proxmox client
	Client = CreateProxmoxClient()
	ctx    = context.Background()
)

type ProxmoxConfig struct {
	Endpoint              string
	APIEndpoint           string
	InsecureSkipTLSVerify bool
	Username              string
	Password              string
	TokenID               string
	Secret                string
}

func CreateProxmoxClient() *proxmox.Client {
	// Create a new client
	endpoint := os.Getenv("PROXMOX_ENDPOINT")
	ProxmoxConfig := &ProxmoxConfig{
		APIEndpoint: fmt.Sprintf("https://%s:8006/api2/json", endpoint),
		Username:    os.Getenv("PROXMOX_USERNAME"),
		Password:    os.Getenv("PROXMOX_PASSWORD"),
		TokenID:     os.Getenv("PROXMOX_TOKEN_ID"),
		Secret:      os.Getenv("PROXMOX_SECRET"),
	}

	var httpClient *http.Client
	ProxmoxConfig.InsecureSkipTLSVerify = os.Getenv("PROXMOX_INSECURE_SKIP_TLS_VERIFY") == "true"
	if ProxmoxConfig.InsecureSkipTLSVerify {
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
	case ProxmoxConfig.Username != "" && ProxmoxConfig.Password != "":
		client = proxmox.NewClient(ProxmoxConfig.APIEndpoint,
			proxmox.WithCredentials(&proxmox.Credentials{
				Username: ProxmoxConfig.Username,
				Password: ProxmoxConfig.Password,
			}),
			proxmox.WithHTTPClient(httpClient),
		)
	case ProxmoxConfig.TokenID != "" && ProxmoxConfig.Secret != "":
		client = proxmox.NewClient(ProxmoxConfig.APIEndpoint,
			proxmox.WithAPIToken(ProxmoxConfig.TokenID, ProxmoxConfig.Secret),
			proxmox.WithHTTPClient(httpClient),
		)
	default:
		panic("Proxmox credentials are not defined")
	}
	return client
}

func GetProxmoxVersion() (*proxmox.Version, error) {
	// Get the version of the Proxmox server
	version, err := Client.Version(ctx)
	if err != nil {
		return nil, err
	}
	return version, nil
}
