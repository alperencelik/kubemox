package kubernetes

import (
	"context"
	"fmt"
	"os"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
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

func CheckCertManagerCRDsExists() (bool, error) {
	// Check if cert-manager crds exist
	crds, err := ListCRDs()
	if err != nil {
		return false, err
	}
	// Return true if cert-manager crds exist
	return utils.ExistsIn(crds, CertManagerCRDs), nil
}

func CreateCertificate(customCert *proxmoxv1alpha1.CustomCertificate) (*unstructured.Unstructured, error) {
	certManagerSpec := customCert.Spec.CertManagerSpec
	commonName := certManagerSpec.CommonName
	issuerRef := certManagerSpec.IssuerRef
	secretName := certManagerSpec.SecretName
	usages := certManagerSpec.Usages

	// Check if secret ref exists
	secretExists, err := checkSecretExists(secretName, customCert.GetNamespace())
	if err != nil {
		return nil, err
	}
	if !secretExists {
		return nil, fmt.Errorf("secret %s does not exist in namespace %s", secretName, customCert.GetNamespace())
	}

	certManagerCertificate := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]any{
				"name":      customCert.GetName(),
				"namespace": customCert.GetNamespace(),
				"ownerReferences": []map[string]any{
					{
						"apiVersion": customCert.APIVersion,
						"kind":       customCert.Kind,
						"name":       customCert.GetName(),
						"uid":        customCert.GetUID(),
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

	_, err = DynamicClient.Resource(certificateGVR).Namespace(customCert.ObjectMeta.Namespace).Create(
		context.Background(), certManagerCertificate, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return certManagerCertificate, err
}

func CheckCertificateExists(customCertName, customCertNamespace string) (bool, error) {
	// Check if certificate exists
	certificateName := customCertName
	_, err := DynamicClient.Resource(certificateGVR).Namespace(customCertNamespace).Get(
		context.Background(), certificateName, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

func GetCertificate(customCert *proxmoxv1alpha1.CustomCertificate) (*unstructured.Unstructured, error) {
	// Get certificate
	certificateName := customCert.Name
	certificateNamespace := customCert.Namespace
	certificate, err := DynamicClient.Resource(certificateGVR).Namespace(certificateNamespace).Get(
		context.Background(), certificateName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return certificate, nil
}

func UpdateCertificate(customCertSpec *proxmoxv1alpha1.CertManagerSpec, certificate *unstructured.Unstructured) error {
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
			return err
		}
	}
	return nil
}

func WaitForCertificateReady(certificate *certmanagerv1.Certificate) (bool, error) {
	// Check if certificate is ready
	conditionLen := len(certificate.Status.Conditions)
	if conditionLen == 0 {
		return false, nil
	}
	lastCondition := certificate.Status.Conditions[conditionLen-1]
	if lastCondition.Type == certmanagerv1.CertificateConditionReady && lastCondition.Status == "True" {
		return true, nil
	}
	return false, nil
}

func GetCertificateSecretKeys(certificate *certmanagerv1.Certificate) (tlscrt, tlskey []byte, err error) {
	// Get certificate secret
	secretName := certificate.Spec.SecretName
	//  Get Kubernetes secret
	secret, err := Clientset.CoreV1().Secrets(os.Getenv("POD_NAMESPACE")).
		Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}
	// Get the tls.crt and tls.key from the secret
	tlsCrt := secret.Data["tls.crt"]
	tlsKey := secret.Data["tls.key"]
	return tlsCrt, tlsKey, nil
}

func checkSecretExists(secretName, namespace string) (bool, error) {
	// Check if secret exists
	_, err := Clientset.CoreV1().Secrets(namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	switch {
	case errors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}
