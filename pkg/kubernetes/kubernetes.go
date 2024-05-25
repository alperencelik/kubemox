package kubernetes

import (
	"context"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	Clientset, DynamicClient = GetKubeconfig()
)

func ListCRDs() []string {
	// Create apiextensions client
	config := ClientConfig().(*rest.Config)
	// create the clientset
	clientset, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	crds, err := clientset.ApiextensionsV1().CustomResourceDefinitions().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	var crdNames []string
	for i := range crds.Items {
		crdNames = append(crdNames, crds.Items[i].Name)
	}
	// Return CRD names
	return crdNames
}

func GetManagedVMCRD() v1.CustomResourceDefinition {
	config := ClientConfig().(*rest.Config)
	// create the clientset
	clientset, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	crd, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(),
		"managedvirtualmachines.proxmox.alperen.cloud", metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "Failed to get CRD")
	}
	return *crd
}
