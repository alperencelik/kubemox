package proxmox

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	testConnName      = "test-conn"
	testTokenConnName = "test-token-conn"
)

func TestNewProxmoxClientFromRef(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:            testConnName,
			ResourceVersion: "1",
		},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://localhost:8006",
			Username: "root",
			Password: "password",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	// Test 1: First call - should create new client
	client1, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: testConnName})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test 2: Second call - should return cached client
	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: testConnName})
	if err != nil {
		t.Fatalf("Failed to retrieve cached client: %v", err)
	}

	if client1 != client2 {
		t.Errorf("Expected cached client to be returned, but got different instance")
	}

	// Test 3: Update ResourceVersion - should create new client
	// We need to get the latest version of the object
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), conn); err != nil {
		t.Fatalf("Failed to get latest connection: %v", err)
	}

	conn.Spec.Username = "updated"
	if err := cl.Update(context.Background(), conn); err != nil {
		t.Fatalf("Failed to update connection: %v", err)
	}

	// Get the updated connection to check the new ResourceVersion
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(conn), conn); err != nil {
		t.Fatalf("Failed to get updated connection: %v", err)
	}

	client3, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: testConnName})
	if err != nil {
		t.Fatalf("Failed to create new client after update: %v", err)
	}

	if client1 == client3 {
		t.Errorf("Expected new client after update, but got cached instance")
	}

	// Test 4: Verify cache is updated
	client4, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: testConnName})
	if err != nil {
		t.Fatalf("Failed to retrieve updated cached client: %v", err)
	}

	if client3 != client4 {
		t.Errorf("Expected updated cached client to be returned, but got different instance")
	}

	// Test 5: TTL expiration - should create new client for session-based (username/password) auth
	clientCacheMutex.Lock()
	if cached, ok := clientCache[testConnName]; ok {
		cached.CreatedAt = time.Now().Add(-2 * clientCacheTTL)
	}
	clientCacheMutex.Unlock()

	client5, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: testConnName})
	if err != nil {
		t.Fatalf("Failed to create client after TTL expiry: %v", err)
	}

	if client4 == client5 {
		t.Errorf("Expected new client after TTL expiry for session-based auth, but got cached instance")
	}
}

func TestNewProxmoxClientFromRef_APITokenNoTTL(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:            testTokenConnName,
			ResourceVersion: "1",
		},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://localhost:8006",
			TokenID:  "root@pam!mytoken",
			Secret:   "some-secret-value",
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(conn).Build()

	// Create initial client
	client1, err := NewProxmoxClientFromRef(context.Background(),
		cl, &corev1.LocalObjectReference{Name: testTokenConnName})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Expire the cache entry's CreatedAt
	clientCacheMutex.Lock()
	if cached, ok := clientCache[testTokenConnName]; ok {
		cached.CreatedAt = time.Now().Add(-2 * clientCacheTTL)
	}
	clientCacheMutex.Unlock()

	// Should still return cached client since API tokens don't use session TTL
	client2, err := NewProxmoxClientFromRef(context.Background(),
		cl, &corev1.LocalObjectReference{Name: testTokenConnName})
	if err != nil {
		t.Fatalf("Failed to retrieve cached client: %v", err)
	}

	if client1 != client2 {
		t.Errorf("Expected cached client for API token auth (no TTL), but got new instance")
	}
}

// TestNewProxmoxClientFromRef_SecretRefRefreshes pins that a client built from
// Secret-backed credentials is not reused forever. After secretRefCacheTTL the
// Secret is read again, so a rotated token is picked up without touching the
// ProxmoxConnection - and a Secret that has gone away surfaces as an error
// rather than as a stale client that keeps working until it doesn't.
func TestNewProxmoxClientFromRef_SecretRefRefreshes(t *testing.T) {
	const name = "test-secretref-conn"
	ctx := context.Background()
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: "1"},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: "https://localhost:8006", TokenID: "root@pam!kubemox", SecretFrom: secretKeyRef("token"),
		},
	}
	cl := credentialsClient(t, conn, credentialsSecret(map[string]string{"token": "first-token"}))
	ref := &corev1.LocalObjectReference{Name: name}
	age := func() {
		clientCacheMutex.Lock()
		defer clientCacheMutex.Unlock()
		if cached, ok := clientCache[name]; ok {
			cached.CreatedAt = time.Now().Add(-2 * secretRefCacheTTL)
		}
	}

	c1, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("first client: %v", err)
	}
	c2, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	if c1 != c2 {
		t.Fatal("within the refresh interval the cached client must be reused")
	}

	// Rotate the token and age the cache entry past the interval.
	live := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: credNamespace, Name: credSecretName}, live); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	live.Data["token"] = []byte("rotated-token")
	if err := cl.Update(ctx, live); err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	age()

	c3, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("client after rotation: %v", err)
	}
	if c3 == c2 {
		t.Fatal("after the refresh interval a client built from a Secret must be rebuilt")
	}

	// Remove the Secret; once the entry is stale again the next call must fail.
	if err := cl.Delete(ctx, live); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	age()
	if _, err := NewProxmoxClientFromRef(ctx, cl, ref); err == nil {
		t.Fatal("expected an error once the referenced Secret is gone")
	}
}
