package kubernetes

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var (
	Clientset, DynamicClient = GetKubeconfig()
)

const (
	eventTypeNormal = "Normal"
)

func InsideCluster() bool {
	// Check if kubeconfig exists under home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err.Error())
	}
	kubeconfig := filepath.Join(homeDir, ".kube", "config")

	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		// kubeconfig doesn't exist
		return true
	}
	return false
}

func ClientConfig() any {
	if InsideCluster() {
		config, err := rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}
		return config
	} else {
		var kubeconfig *string
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()
		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
		return config
	}
}

func GetKubeconfig() (*kubernetes.Clientset, dynamic.Interface) {
	config := ClientConfig().(*rest.Config)
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

func CreateVMKubernetesEvent(vm *proxmoxv1alpha1.VirtualMachine, clientset *kubernetes.Clientset, action string) {
	// Create a new event
	Event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", vm.ObjectMeta.Name, action, time.Now()),
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
