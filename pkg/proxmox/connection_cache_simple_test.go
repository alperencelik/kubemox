package proxmox

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func createTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	return scheme
}

func createTestProxmoxConnection(
	name, resourceVersion string,
	spec proxmoxv1alpha1.ProxmoxConnectionSpec,
) *proxmoxv1alpha1.ProxmoxConnection {
	return &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			ResourceVersion: resourceVersion,
		},
		Spec: spec,
	}
}

func TestNewProxmoxClientFromRef_NilReference(t *testing.T) {
	scheme := createTestScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, nil)

	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ProxmoxConnection reference is nil or empty")
}

func TestNewProxmoxClientFromRef_EmptyName(t *testing.T) {
	scheme := createTestScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: ""})

	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ProxmoxConnection reference is nil or empty")
}

func TestNewProxmoxClientFromRef_ConnectionNotFound(t *testing.T) {
	scheme := createTestScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "nonexistent"})

	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), `getting ProxmoxConnection "nonexistent"`)
	assert.Contains(t, err.Error(), "not found")
}

func TestNewProxmoxClientFromRef_CacheHit_SameResourceVersion(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			Username: "root",
			Password: "password",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	client1, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.NotNil(t, client1)

	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.NotNil(t, client2)

	assert.Same(t, client1, client2, "Expected cached client to be returned")
}

func TestNewProxmoxClientFromRef_CacheMiss_FirstCall(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			Username: "root",
			Password: "password",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.NotNil(t, client)

	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.Same(t, client, client2, "Second call should return cached client")
}

func TestNewProxmoxClientFromRef_UsernamePasswordAuth(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			Username: "root@pam",
			Password: "secret-password",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
}

func TestNewProxmoxClientFromRef_TokenAuth(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			TokenID:  "kubemox-token",
			Secret:   "token-secret",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
}

func TestNewProxmoxClientFromRef_TokenAuth_Cached(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			TokenID:  "kubemox-token",
			Secret:   "token-secret",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	client1, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)

	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	assert.NoError(t, err)

	assert.Same(t, client1, client2, "Token-authenticated client should also be cached")
}

func TestNewProxmoxClientFromRef_ConcurrentCalls_SameConnection(t *testing.T) {
	scheme := createTestScheme()
	conn := createTestProxmoxConnection(
		"test-conn",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox:8006",
			Username: "root",
			Password: "password",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	var clients []*ProxmoxClient
	var mu sync.Mutex
	var wg sync.WaitGroup

	numGoroutines := 20
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			client, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
			assert.NoError(t, err)
			if client != nil {
				mu.Lock()
				clients = append(clients, client)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	assert.Len(t, clients, numGoroutines, "All goroutines should get a client")

	firstClient := clients[0]
	for i := 1; i < len(clients); i++ {
		assert.Same(t, firstClient, clients[i], "All concurrent calls should return the same cached client")
	}
}

func TestNewProxmoxClientFromRef_MultipleConnections_DifferentInstances(t *testing.T) {
	scheme := createTestScheme()

	conn1 := createTestProxmoxConnection(
		"conn-1",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox1:8006",
			Username: "root",
			Password: "password1",
		},
	)

	conn2 := createTestProxmoxConnection(
		"conn-2",
		"1",
		proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://proxmox2:8006",
			Username: "root",
			Password: "password2",
		},
	)

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn1, conn2).Build()

	client1, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "conn-1"})
	assert.NoError(t, err)

	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "conn-2"})
	assert.NoError(t, err)

	assert.NotSame(t, client1, client2, "Different connections should create different client instances")

	// Verify caching works independently
	client1Cached, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "conn-1"})
	assert.NoError(t, err)
	assert.Same(t, client1, client1Cached, "Should return cached client for conn-1")

	client2Cached, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "conn-2"})
	assert.NoError(t, err)
	assert.Same(t, client2, client2Cached, "Should return cached client for conn-2")
}
