package kubernetes

import (
	"context"
	"fmt"
	"os"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	eventTypeNormal = "Normal"
)

func CreateVMKubernetesEvent(vm *proxmoxv1alpha1.VirtualMachine, clientset *kubernetes.Clientset, action string) {
	// Create a new event
	Event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", vm.ObjectMeta.Name, action, time.Now()),
			Namespace: vm.ObjectMeta.Namespace,
			Labels: map[string]string{
				"app": "kubemox",
			},
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: vm.APIVersion,
			Kind:       vm.Kind,
			Namespace:  vm.ObjectMeta.Namespace,
			Name:       vm.ObjectMeta.Name,
			UID:        vm.ObjectMeta.UID,
		},
		Source: corev1.EventSource{
			Component: "kubemox",
		},
		FirstTimestamp: metav1.Time{Time: time.Now()},
	}
	switch action {
	case "Created":
		Event.Reason = "Created"
		Event.Message = fmt.Sprintf("VirtualMachine %s has been created", vm.Spec.Name)
		Event.Type = eventTypeNormal
	case "Creating":
		Event.Reason = "Creating"
		Event.Message = fmt.Sprintf("VirtualMachine %s is being created", vm.Spec.Name)
		Event.Type = eventTypeNormal
	case "Deleting":
		Event.Reason = "Deleting"
		Event.Message = fmt.Sprintf("VirtualMachine %s is being deleted", vm.Spec.Name)
		Event.Type = eventTypeNormal
	default:
		// Do nothing
	}

	_, err := clientset.CoreV1().Events(vm.ObjectMeta.Namespace).Create(context.Background(), Event, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}
}

func CreateManagedVMKubernetesEvent(managedVM *proxmoxv1alpha1.ManagedVirtualMachine, clientset *kubernetes.Clientset, action string) {
	// Create event
	Event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", managedVM.Name, action, time.Now()),
			Namespace: os.Getenv("POD_NAMESPACE"),
			Labels: map[string]string{
				"app": "kubemox",
			},
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: managedVM.APIVersion,
			Kind:       managedVM.Kind,
			Namespace:  os.Getenv("POD_NAMESPACE"),
			Name:       managedVM.ObjectMeta.Name,
			UID:        managedVM.ObjectMeta.UID,
		},
		Source: corev1.EventSource{
			Component: "kubemox",
		},
		FirstTimestamp: metav1.Time{Time: time.Now()},
		Reason:         action,
		Message:        fmt.Sprintf("ManagedVirtualMachine %s has been %s", managedVM.Name, action),
		Type:           "Normal",
	}
	// Send event
	_, err := clientset.CoreV1().Events(os.Getenv("POD_NAMESPACE")).Create(context.Background(), Event, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}
}
