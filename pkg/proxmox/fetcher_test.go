package proxmox

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// TestUpdateResourceStatus_FailureLeavesStatusUntouched verifies that when the
// fetcher cannot reach Proxmox (here simulated by a missing ProxmoxConnection),
// it returns an error without overwriting the previously-observed status or
// LastObserved timestamp. That is what surfaces staleness to consumers — if
// the operator silently re-stamped LastObserved on failure, kubectl could not
// distinguish "still verified" from "haven't reached Proxmox in hours".
func TestVirtualMachineFetcher_UpdateResourceStatus_FailureLeavesStatusUntouched(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	// metav1.Time serializes at second precision; pre-truncate so the round-trip
	// through the fake client's storage is bit-equal to the value we wrote.
	earlier := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	vm := &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "vm-with-broken-conn",
			Generation: 4,
		},
		Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name:          "vm-with-broken-conn",
			NodeName:      "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: "missing-conn"},
		},
		Status: proxmoxv1alpha1.VirtualMachineStatus{
			Status:             &proxmoxv1alpha1.QEMUStatus{State: "running"},
			LastObserved:       &earlier,
			ObservedGeneration: 3,
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&proxmoxv1alpha1.VirtualMachine{}).
		WithObjects(vm).
		Build()

	f := &VirtualMachineFetcher{Client: cl}
	err := f.UpdateResourceStatus(context.Background(), types.NamespacedName{Name: vm.Name}, nil)
	if err == nil {
		t.Fatal("expected UpdateResourceStatus to error when ProxmoxConnection is missing")
	}

	got := &proxmoxv1alpha1.VirtualMachine{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: vm.Name}, got); err != nil {
		t.Fatalf("re-getting VM: %v", err)
	}
	if got.Status.Status == nil || got.Status.Status.State != "running" {
		t.Errorf("Status.Status.State changed unexpectedly on failure: got %+v", got.Status.Status)
	}
	if got.Status.LastObserved == nil || !got.Status.LastObserved.Time.Equal(earlier.Time) {
		t.Errorf("LastObserved must not advance on failure: got %v, want %v",
			got.Status.LastObserved, earlier.Time)
	}
	if got.Status.ObservedGeneration != 3 {
		t.Errorf("ObservedGeneration must not advance on failure: got %d, want 3",
			got.Status.ObservedGeneration)
	}
}

func TestContainerFetcher_UpdateResourceStatus_FailureLeavesStatusUntouched(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	earlier := metav1.NewTime(time.Now().Add(-2 * time.Hour).Truncate(time.Second))
	ct := &proxmoxv1alpha1.Container{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "ct-with-broken-conn",
			Generation: 9,
		},
		Spec: proxmoxv1alpha1.ContainerSpec{
			Name:          "ct-with-broken-conn",
			NodeName:      "pve1",
			ConnectionRef: &corev1.LocalObjectReference{Name: "missing-conn"},
		},
		Status: proxmoxv1alpha1.ContainerStatus{
			Status:             proxmoxv1alpha1.QEMUStatus{State: "running"},
			LastObserved:       &earlier,
			ObservedGeneration: 8,
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&proxmoxv1alpha1.Container{}).
		WithObjects(ct).
		Build()

	f := &ContainerFetcher{Client: cl}
	err := f.UpdateResourceStatus(context.Background(), types.NamespacedName{Name: ct.Name}, nil)
	if err == nil {
		t.Fatal("expected UpdateResourceStatus to error when ProxmoxConnection is missing")
	}

	got := &proxmoxv1alpha1.Container{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: ct.Name}, got); err != nil {
		t.Fatalf("re-getting Container: %v", err)
	}
	if got.Status.Status.State != "running" {
		t.Errorf("Status.Status.State changed unexpectedly on failure: got %+v", got.Status.Status)
	}
	if got.Status.LastObserved == nil || !got.Status.LastObserved.Time.Equal(earlier.Time) {
		t.Errorf("LastObserved must not advance on failure: got %v, want %v",
			got.Status.LastObserved, earlier.Time)
	}
	if got.Status.ObservedGeneration != 8 {
		t.Errorf("ObservedGeneration must not advance on failure: got %d, want 8",
			got.Status.ObservedGeneration)
	}
}
