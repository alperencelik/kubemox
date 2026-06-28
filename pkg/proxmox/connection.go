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
	node          *proxmox.Node
	vms           map[string]int                  // vmName -> vmID
	vmObjs        map[int]*proxmox.VirtualMachine // vmID -> VirtualMachine object
	containers    map[string]int                  // containerName -> containerID
	containerObjs map[int]*proxmox.Container      // containerID -> Container object
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
			proxmox.WithUserAgent("kubemox"),
			// DEBUG
			// proxmox.WithLogger(&proxmox.LeveledLogger{Level: proxmox.LevelDebug}),
		)
	case proxmoxConfig.TokenID != "" && proxmoxConfig.Secret != "":
		client = proxmox.NewClient(proxmoxConfig.Endpoint,
			proxmox.WithAPIToken(proxmoxConfig.TokenID, proxmoxConfig.Secret),
			proxmox.WithHTTPClient(httpClient),
			proxmox.WithUserAgent("kubemox"),
			// DEBUG
			// proxmox.WithLogger(&proxmox.LeveledLogger{Level: proxmox.LevelDebug}),
		)
	}

	return &ProxmoxClient{
		Client:     client,
		nodesCache: make(map[string]NodeCache),
	}
}

func (pc *ProxmoxClient) GetVersion() (*string, error) {
	if pc.Client == nil {
		return nil, fmt.Errorf("proxmox client is not initialized")
	}
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
	pc.nodesCache[nodeName] = NodeCache{
		node:       node,
		vms:        make(map[string]int),
		containers: make(map[string]int),
	}
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
		pc.nodesCache[nodeName] = NodeCache{vms: make(map[string]int), vmObjs: make(map[int]*proxmox.VirtualMachine)}
	}
	pc.nodesCache[nodeName].vms[vmName] = vmID
}

func (pc *ProxmoxClient) setCachedVM(nodeName string, vmID int, vm *proxmox.VirtualMachine) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	if _, exists := pc.nodesCache[nodeName]; !exists {
		pc.nodesCache[nodeName] = NodeCache{vms: make(map[string]int), vmObjs: make(map[int]*proxmox.VirtualMachine)}
	} else if pc.nodesCache[nodeName].vmObjs == nil {
		// Initialize mapping if nil (for existing cache entries before upgrade)
		entry := pc.nodesCache[nodeName]
		entry.vmObjs = make(map[int]*proxmox.VirtualMachine)
		pc.nodesCache[nodeName] = entry
	}
	pc.nodesCache[nodeName].vmObjs[vmID] = vm
}

func (pc *ProxmoxClient) getCachedVM(nodeName string, vmID int) *proxmox.VirtualMachine {
	pc.vmIDMutex.RLock()
	defer pc.vmIDMutex.RUnlock()
	if nodeCache, exists := pc.nodesCache[nodeName]; exists && nodeCache.vmObjs != nil {
		return nodeCache.vmObjs[vmID]
	}
	return nil
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
	// Only retry idempotent, safe methods. The Proxmox API uses POST for
	// create/clone/start/stop/snapshot/delete operations — retrying those
	// after a connection drop mid-flight can re-submit the request and
	// spawn duplicate tasks or VMs. GET/HEAD have no side effects and are
	// always safe to retry on transient transport errors.
	if !isRetryableMethod(req.Method) {
		return rt.Transport.RoundTrip(req)
	}

	var resp *http.Response
	var err error

	for i := 0; i <= rt.Retries; i++ {
		resp, err = rt.Transport.RoundTrip(req)
		if err == nil {
			return resp, nil
		}

		// Retry on transient connection errors that occur before the server
		// processes the request. These are safe to retry for idempotent
		// methods (guarded above).
		if isRetriableError(err) {
			continue
		}

		// Non-retriable error: return immediately
		return resp, err
	}
	return resp, err
}

// isRetryableMethod reports whether the HTTP method is safe to retry on
// transient connection errors. Only idempotent, body-less methods are
// retried; PVE mutative endpoints (POST/PUT/DELETE/PATCH) must not be
// retried to avoid duplicate tasks or VMs when the server may have
// already received and processed the request.
func isRetryableMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// isRetriableError reports whether the transport error is safe to retry
// for an idempotent request. These errors indicate the connection dropped
// before or during the request handshake, before the server could act on it.
func isRetriableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "http: server closed idle connection") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "unexpected end of JSON input") ||
		strings.Contains(msg, "connection reset by peer") {
		return true
	}
	return false
}
