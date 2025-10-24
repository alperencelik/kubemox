package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
)

type ProxmoxClient struct {
	Client *proxmox.Client
	// ctx    context.Context
	// Cache for nodes to reduce API calls
	nodesCache map[string]*proxmox.Node
	nodesMutex sync.RWMutex
	// Cache for VM IDs to reduce API calls
	vmIDCache map[string]map[string]int // nodeName -> vmName -> vmID
	vmIDMutex sync.RWMutex
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

	return &ProxmoxClient{
		Client:     client,
		nodesCache: make(map[string]*proxmox.Node),
		vmIDCache:  make(map[string]map[string]int),
	}
}

func (pc *ProxmoxClient) GetVersion() (*string, error) {
	version, err := pc.Client.Version(context.Background())
	if err != nil {
		return nil, err
	}
	return &version.Version, nil
}

// getNode retrieves a node from cache if available, otherwise fetches it from Proxmox API
func (pc *ProxmoxClient) getNode(ctx context.Context, nodeName string) (*proxmox.Node, error) {
	// Try to get from cache first
	pc.nodesMutex.RLock()
	if node, exists := pc.nodesCache[nodeName]; exists {
		pc.nodesMutex.RUnlock()
		return node, nil
	}
	pc.nodesMutex.RUnlock()

	// Not in cache, fetch from API
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	// Store in cache
	pc.nodesMutex.Lock()
	pc.nodesCache[nodeName] = node
	pc.nodesMutex.Unlock()

	return node, nil
}

// refreshNodeCache refreshes the node cache
func (pc *ProxmoxClient) refreshNodeCache(ctx context.Context, nodeName string) error {
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return err
	}

	pc.nodesMutex.Lock()
	pc.nodesCache[nodeName] = node
	pc.nodesMutex.Unlock()

	return nil
}

// getCachedVMID retrieves a VM ID from cache
func (pc *ProxmoxClient) getCachedVMID(nodeName, vmName string) (int, bool) {
	pc.vmIDMutex.RLock()
	defer pc.vmIDMutex.RUnlock()

	if nodeCache, exists := pc.vmIDCache[nodeName]; exists {
		if vmID, exists := nodeCache[vmName]; exists {
			return vmID, true
		}
	}
	return 0, false
}

// setCachedVMID stores a VM ID in cache
func (pc *ProxmoxClient) setCachedVMID(nodeName, vmName string, vmID int) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()

	if _, exists := pc.vmIDCache[nodeName]; !exists {
		pc.vmIDCache[nodeName] = make(map[string]int)
	}
	pc.vmIDCache[nodeName][vmName] = vmID
}

// refreshVMIDCache refreshes the VM ID cache for a specific node
func (pc *ProxmoxClient) refreshVMIDCache(ctx context.Context, nodeName string) error {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return err
	}

	vmList, err := node.VirtualMachines(ctx)
	if err != nil {
		return err
	}

	pc.vmIDMutex.Lock()
	if _, exists := pc.vmIDCache[nodeName]; !exists {
		pc.vmIDCache[nodeName] = make(map[string]int)
	}
	for _, vm := range vmList {
		pc.vmIDCache[nodeName][vm.Name] = int(vm.VMID)
	}
	pc.vmIDMutex.Unlock()

	return nil
}

func NewProxmoxClientFromRef(ctx context.Context, c cc.Client,
	ref *corev1.LocalObjectReference) (*ProxmoxClient, error) {
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("ProxmoxConnection reference is nil or empty")
	}
	conn := &proxmoxv1alpha1.ProxmoxConnection{}
	if err := c.Get(ctx, cc.ObjectKey{Name: ref.Name}, conn); err != nil {
		return nil, fmt.Errorf("getting ProxmoxConnection %q: %w", ref.Name, err)
	}
	return NewProxmoxClient(conn), nil
}
