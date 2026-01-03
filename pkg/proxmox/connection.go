package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/luthermonson/go-proxmox"
	corev1 "k8s.io/api/core/v1"
	cc "sigs.k8s.io/controller-runtime/pkg/client"
)

type ProxmoxClient struct {
	Client     *proxmox.Client
	nodesCache map[string]NodeCache
	nodesMutex sync.RWMutex
	vmIDMutex  sync.RWMutex
}

var (
	clientCache      = make(map[string]*CachedClient)
	clientCacheMutex sync.RWMutex
)

type CachedClient struct {
	Client          *ProxmoxClient
	ResourceVersion string
}

type NodeCache struct {
	node *proxmox.Node
	vms  map[string]int // vmName -> vmID
}

func NewProxmoxClient(proxmoxConnection *proxmoxv1alpha1.ProxmoxConnection) *ProxmoxClient {
	// Create a new client
	proxmoxConfig := proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint:           getProxmoxAPIEndpoint(proxmoxConnection.Spec.Endpoint),
		Username:           proxmoxConnection.Spec.Username,
		Password:           proxmoxConnection.Spec.Password,
		TokenID:            proxmoxConnection.Spec.TokenID,
		Secret:             proxmoxConnection.Spec.Secret,
		InsecureSkipVerify: proxmoxConnection.Spec.InsecureSkipVerify,
	}
	var tlsConfig *tls.Config
	if proxmoxConfig.InsecureSkipVerify {
		tlsConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user choice
	} else {
		tlsConfig = &tls.Config{}
	}
	// Configure HTTP client with performance tuning
	transport := &http.Transport{
		MaxIdleConns:        100,
		DisableKeepAlives:   false,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     30 * time.Second,
		TLSClientConfig:     tlsConfig,
	}

	httpClient := &http.Client{
		Transport: &RetryRoundTripper{
			Transport: transport,
			Retries:   3,
		},
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
		nodesCache: make(map[string]NodeCache),
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
		return node.node, nil
	}
	pc.nodesMutex.RUnlock()

	// Not in cache, fetch from API
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	// Store in cache
	pc.nodesMutex.Lock()
	pc.nodesCache[nodeName] = NodeCache{node: node, vms: make(map[string]int)}
	pc.nodesMutex.Unlock()

	return node, nil
}

func NewProxmoxClientFromRef(ctx context.Context, c cc.Client,
	ref *corev1.LocalObjectReference) (*ProxmoxClient, error) {
	if ref == nil || ref.Name == "" {
		return nil, fmt.Errorf("ProxmoxConnection reference is nil or empty")
	}

	// Check the cache first
	clientCacheMutex.RLock()
	cached, exists := clientCache[ref.Name]
	clientCacheMutex.RUnlock()

	conn := &proxmoxv1alpha1.ProxmoxConnection{}
	if err := c.Get(ctx, cc.ObjectKey{Name: ref.Name}, conn); err != nil {
		return nil, fmt.Errorf("getting ProxmoxConnection %q: %w", ref.Name, err)
	}

	// If exists in cache and ResourceVersion matches, return cached client
	if exists && cached.ResourceVersion == conn.ResourceVersion {
		return cached.Client, nil
	}

	// If not in cache or ResourceVersion mismatch, create new client
	client := NewProxmoxClient(conn)

	// Update cache
	clientCacheMutex.Lock()
	clientCache[ref.Name] = &CachedClient{
		Client:          client,
		ResourceVersion: conn.ResourceVersion,
	}
	clientCacheMutex.Unlock()

	return client, nil
}

func (pc *ProxmoxClient) setCachedVMID(nodeName, vmName string, vmID int) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	if _, exists := pc.nodesCache[nodeName]; !exists {
		pc.nodesCache[nodeName] = NodeCache{vms: make(map[string]int)}
	}
	pc.nodesCache[nodeName].vms[vmName] = vmID
}

func getProxmoxAPIEndpoint(endpoint string) string {
	// Check if the endpoint already contains the API path
	if strings.HasSuffix(endpoint, "/api2/json") {
		if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
			return endpoint
		} else {
			return "https://" + endpoint
		}
	}
	// Append the API path if not present
	return fmt.Sprintf("https://%s:8006/api2/json", endpoint)
}

// RetryRoundTripper is a custom RoundTripper that retries requests on specific errors
type RetryRoundTripper struct {
	Transport http.RoundTripper
	Retries   int
}

func (rt *RetryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for i := 0; i <= rt.Retries; i++ {
		resp, err = rt.Transport.RoundTrip(req)
		if err == nil {
			return resp, nil
		}

		// Check if the error is "server closed idle connection"
		// This error is safe to retry for idempotent requests, and also for POSTs if we are sure it happened before the body was sent.
		// In Go's net/http, this error specifically means the connection was closed by the peer while trying to reuse it.
		// It is generally safe to retry this specific error.
		if strings.Contains(err.Error(), "http: server closed idle connection") ||
			strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "unexpected end of JSON input") {
			// If we have a body, we might need to rewind it, but GetBody is handled by Request
			if req.GetBody != nil {
				newBody, bodyErr := req.GetBody()
				if bodyErr != nil {
					return resp, err
				}
				req.Body = newBody
			}
			continue
		}

		// Provide a fallback for connection reset by peer which often happens with Proxmox
		if strings.Contains(err.Error(), "connection reset by peer") {
			if req.GetBody != nil {
				newBody, bodyErr := req.GetBody()
				if bodyErr != nil {
					return resp, err
				}
				req.Body = newBody
			}
			continue
		}

		// If it's not a retriable error, return immediately
		return resp, err
	}
	return resp, err
}
