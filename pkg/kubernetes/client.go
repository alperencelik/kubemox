package kubernetes

import (
	"flag"
	"os"
	"path/filepath"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// getKubeClients lazily initializes and caches the Kubernetes clients on first access.
// to success unit tests without requiring a kubeconfig or in-cluster config.
var getKubeClients = sync.OnceValues(GetKubeconfig)

// Clientset returns the shared Kubernetes clientset, initializing it on first call.
func Clientset() *kubernetes.Clientset {
	cs, _ := getKubeClients()
	return cs
}

// DynamicClient returns the shared dynamic client, initializing it on first call.
func DynamicClient() dynamic.Interface {
	_, dc := getKubeClients()
	return dc
}

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

func ClientConfig() *rest.Config {
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
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"),
				"(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
		return config
	}
}

func GetKubeconfig() (*kubernetes.Clientset, dynamic.Interface) {
	config := ClientConfig()
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
