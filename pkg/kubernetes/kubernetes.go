package kubernetes

import (
	"context"
	"fmt"
	"os"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	Clientset, DynamicClient = GetKubeconfig()
)

func GetKubeconfig() (*kubernetes.Clientset, dynamic.Interface) {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	// Dynamic client
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return clientset, dynamicClient
}

func CreateVMKubernetesEvent(vm *proxmoxv1alpha1.VirtualMachine, Clientset *kubernetes.Clientset, Action string) {
	// Create a new event
	Event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", vm.ObjectMeta.Name, Action, time.Now()),
			Namespace: vm.ObjectMeta.Namespace,
			Labels: map[string]string{
				"app": "kube-proxmox-operator",
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
			Component: "kube-proxmox-operator",
		},
		FirstTimestamp: metav1.Time{Time: time.Now()},
	}
	if Action == "Created" {
		Event.Reason = "Created"
		Event.Message = fmt.Sprintf("VirtualMachine %s has been created", vm.Spec.Name)
		Event.Type = "Normal"
	} else if Action == "Creating" {
		Event.Reason = "Creating"
		Event.Message = fmt.Sprintf("VirtualMachine %s is being creating", vm.Spec.Name)
		Event.Type = "Normal"
	} else if Action == "Deleting" {
		Event.Reason = "Deleting"
		Event.Message = fmt.Sprintf("VirtualMachine %s is being deleted", vm.Spec.Name)
		Event.Type = "Normal"
	}

	_, err := Clientset.CoreV1().Events(vm.ObjectMeta.Namespace).Create(context.Background(), Event, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}
}

func CreateManagedVMKubernetesEvent(managedVM *proxmoxv1alpha1.ManagedVirtualMachine, Clientset *kubernetes.Clientset, Action string) {
	// Create event
	Event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", managedVM.Name, Action, time.Now()),
			Namespace: os.Getenv("POD_NAMESPACE"),
			Labels: map[string]string{
				"app": "kube-proxmox-operator",
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
			Component: "kube-proxmox-operator",
		},
		FirstTimestamp: metav1.Time{Time: time.Now()},
		Reason:         Action,
		Message:        fmt.Sprintf("ManagedVirtualMachine %s has been %s", managedVM.Name, Action),
		Type:           "Normal",
	}
	// Send event
	_, err := Clientset.CoreV1().Events(os.Getenv("POD_NAMESPACE")).Create(context.Background(), Event, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}
}
