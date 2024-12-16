package kubernetes

import (
	"context"
	"fmt"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	// Cert Manager Resources
	CertManagerCRDs = []string{"certificates.cert-manager.io", "issuers.cert-manager.io", "clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io", "challenges.acme.cert-manager.io", "orders.acme.cert-manager.io"}
	certificateGVR = schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}
)

func CheckCertManagerCRDsExists() bool {
	// Check if cert-manager crds exist
	crds := ListCRDs()
	// Return true if cert-manager crds exist
	return utils.ExistsIn(crds, CertManagerCRDs)
}

func CreateCertificate(customCert *proxmoxv1alpha1.CustomCertificate) (*unstructured.Unstructured, error) {
	certManagerSpec := customCert.Spec.CertManagerSpec
	commonName := certManagerSpec.CommonName
	issuerRef := certManagerSpec.IssuerRef
	secretName := certManagerSpec.SecretName
	usages := certManagerSpec.Usages

	// Check if secret ref exists
	if !CheckSecretExists(secretName, customCert.ObjectMeta.Namespace) {
		return nil, fmt.Errorf("secret %s does not exist in namespace %s", secretName, customCert.ObjectMeta.Namespace)
	}

	certManagerCertificate := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]any{
				"name":      customCert.ObjectMeta.Name,
				"namespace": customCert.ObjectMeta.Namespace,
				"ownerReferences": []map[string]any{
					{
						"apiVersion": customCert.APIVersion,
						"kind":       customCert.Kind,
						"name":       customCert.ObjectMeta.Name,
						"uid":        customCert.ObjectMeta.UID,
					},
				},
			},
			"spec": map[string]any{
				"commonName": commonName,
				"dnsNames":   certManagerSpec.DNSNames,
				"issuerRef":  issuerRef,
				"secretName": secretName,
				"usages":     usages,
			},
		},
	}

	_, err := DynamicClient.Resource(certificateGVR).Namespace(customCert.ObjectMeta.Namespace).Create(
		context.Background(), certManagerCertificate, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}

	return certManagerCertificate, err
}

func CheckCertificateExists(customCertName, customCertNamespace string) bool {
	// Check if certificate exists
	certificateName := customCertName
	_, err := DynamicClient.Resource(certificateGVR).Namespace(customCertNamespace).Get(
		context.Background(), certificateName, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return false
	case err != nil:
		panic(err)
	default:
		return true
	}
}

func GetCertificate(customCert *proxmoxv1alpha1.CustomCertificate) *unstructured.Unstructured {
	// Get certificate
	certificateName := customCert.Name
	certificateNamespace := customCert.Namespace
	certificate, err := DynamicClient.Resource(certificateGVR).Namespace(certificateNamespace).Get(
		context.Background(), certificateName, metav1.GetOptions{})
	if err != nil {
		panic(err)
	}
	return certificate
}

func UpdateCertificate(customCertSpec *proxmoxv1alpha1.CertManagerSpec, certificate *unstructured.Unstructured) {
	// Compare actual state and desired state
	// Actual state
	certSpec := certificate.Object["spec"].(map[string]any)
	// Desired state
	customCertMap := map[string]any{
		"commonName": customCertSpec.CommonName,
		"dnsNames":   customCertSpec.DNSNames,
		"issuerRef": map[string]string{
			"group": customCertSpec.IssuerRef.Group,
			"kind":  customCertSpec.IssuerRef.Kind,
			"name":  customCertSpec.IssuerRef.Name,
		},
		"secretName": customCertSpec.SecretName,
		"usages":     customCertSpec.Usages,
	}
	// TODO: reflect.DeepEqual doesn't work for those since they have different struct types
	certSpecStr := fmt.Sprintf("%v", certSpec)
	customCertMapStr := fmt.Sprintf("%v", customCertMap)

	if certSpecStr != customCertMapStr {
		// Update the certificate
		log.Log.Info("Updating the certificate", "Certificate", certificate.GetName())
		certificate.Object["spec"] = customCertMap
		_, err := DynamicClient.Resource(certificateGVR).Namespace(certificate.GetNamespace()).Update(
			context.Background(), certificate, metav1.UpdateOptions{})
		if err != nil {
			panic(err)
		}
	}
}

func GetCertificateSecretKeys(certificate *unstructured.Unstructured) (tlscrt, tlskey []byte) {
	// Get certificate secret
	secretName := certificate.Object["spec"].(map[string]any)["secretName"].(string)
	// Find the namespace that cert-manager is installed and get the secret from that namespace
	certManagerNamespace := findCertManagerNamespace()
	if certManagerNamespace == "" {
		panic("cert-manager deployment is not found in any namespace")
	}
	// Get Kubernetes secret
	secret, err := Clientset.CoreV1().Secrets(certManagerNamespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		panic(err)
	}
	// Get the tls.crt and tls.key from the secret
	tlsCrt := secret.Data["tls.crt"]
	tlsKey := secret.Data["tls.key"]
	return tlsCrt, tlsKey
}

func findCertManagerNamespace() string {
	// Find the namespace that cert-manager is installed
	// List namespaces and find the namespace that has cert-manager resources
	namespaces, err := Clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	// If there is a namespace called cert-manager, return it
	for i := range namespaces.Items {
		namespace := &namespaces.Items[i]
		if namespace.Name == "cert-manager" {
			return namespace.Name
		} else {
			// If there is no namespace called cert-manager, then get all deployments and check if cert-manager is installed
			deployments, err := Clientset.AppsV1().Deployments(namespace.Name).List(context.Background(), metav1.ListOptions{})
			// If there is a deployment called cert-manager, return the namespace
			if err != nil {
				panic(err)
			}
			for j := range deployments.Items {
				deployment := &deployments.Items[j]
				if deployment.Name == "cert-manager" {
					return namespace.Name
				}
			}
		}
	}
	return ""
}

func CheckSecretExists(secretName, namespace string) bool {
	// Check if secret exists
	_, err := Clientset.CoreV1().Secrets(namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return false
	case err != nil:
		panic(err)
	default:
		return true
	}
}
