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
			Endpoint: testLocalURL,
			Username: "root",
			Password: testPassword,
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
			Endpoint: testLocalURL,
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

// TestNewProxmoxClientFromRef_SecretRefFollowsConnection pins how a client
// built from Secret-backed credentials is refreshed now that the refresh
// interval is gone: it is reused until the ProxmoxConnection itself changes.
// Recording a new observed Secret resourceVersion in the status is such a
// change, so the controller noticing a rotation is what rebuilds the client -
// and a Secret that has gone away surfaces as an error rather than as a stale
// client that keeps working until it doesn't.
func TestNewProxmoxClientFromRef_SecretRefFollowsConnection(t *testing.T) {
	const name = "test-secretref-conn"
	ctx := context.Background()
	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: "1"},
		Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
			Endpoint: testLocalURL, TokenID: testTokenID, SecretFrom: secretKeyRef(credTokenKey),
		},
	}
	cl := credentialsClient(t, conn, credentialsSecret(map[string]string{credTokenKey: "first-token"}))
	ref := &corev1.LocalObjectReference{Name: name}

	// observe is what the controller does once it has read the Secret: it
	// publishes the version it saw, which changes the object.
	observe := func(version string) {
		t.Helper()
		live := &proxmoxv1alpha1.ProxmoxConnection{}
		if err := cl.Get(ctx, client.ObjectKey{Name: name}, live); err != nil {
			t.Fatalf("get connection: %v", err)
		}
		live.Status.ObservedSecrets = []proxmoxv1alpha1.ObservedSecret{{
			Name: credSecretName, Namespace: credNamespace, ResourceVersion: version,
		}}
		if err := cl.Status().Update(ctx, live); err != nil {
			t.Fatalf("publish observed secret: %v", err)
		}
	}

	c1, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("first client: %v", err)
	}

	// Age the entry far past the session TTL: an API token has no session, so
	// nothing about the passage of time may rebuild this client any more.
	clientCacheMutex.Lock()
	if cached, ok := clientCache[name]; ok {
		cached.CreatedAt = time.Now().Add(-2 * clientCacheTTL)
	}
	clientCacheMutex.Unlock()

	c2, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	if c1 != c2 {
		t.Fatal("an unchanged connection must keep its cached client, however old it is")
	}

	// Rotate the token; until the controller records it, the client stays.
	live := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: credNamespace, Name: credSecretName}, live); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	live.Data[credTokenKey] = []byte("rotated-token")
	if err := cl.Update(ctx, live); err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	c3, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("client after rotation: %v", err)
	}
	if c3 != c2 {
		t.Fatal("a rotation the controller has not observed yet must not rebuild the client")
	}

	observe(live.ResourceVersion)
	c4, err := NewProxmoxClientFromRef(ctx, cl, ref)
	if err != nil {
		t.Fatalf("client after the rotation was observed: %v", err)
	}
	if c4 == c3 {
		t.Fatal("once the observed resourceVersion changes the client must be rebuilt")
	}

	// Remove the Secret; the next rebuild must fail loudly.
	if err := cl.Delete(ctx, live); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	observe("gone")
	if _, err := NewProxmoxClientFromRef(ctx, cl, ref); err == nil {
		t.Fatal("expected an error once the referenced Secret is gone")
	}
}
