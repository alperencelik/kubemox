package proxmox

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestNewProxmoxClientFromRef(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	conn := &proxmoxv1alpha1.ProxmoxConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-conn",
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
	client1, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test 2: Second call - should return cached client
	client2, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
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

	client3, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	if err != nil {
		t.Fatalf("Failed to create new client after update: %v", err)
	}

	if client1 == client3 {
		t.Errorf("Expected new client after update, but got cached instance")
	}

	// Test 4: Verify cache is updated
	client4, err := NewProxmoxClientFromRef(context.Background(), cl, &corev1.LocalObjectReference{Name: "test-conn"})
	if err != nil {
		t.Fatalf("Failed to retrieve updated cached client: %v", err)
	}

	if client3 != client4 {
		t.Errorf("Expected updated cached client to be returned, but got different instance")
	}
}
