/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxmox

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
)

// CustomCertificateReconciler reconciles a CustomCertificate object
type CustomCertificateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CustomCertificate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *CustomCertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	Clientset, _ := kubernetes.GetKubeconfig()
	// Get crdList and check if the cert-manager is installed
	crdList, err := ApiextensionsV1().CustomResourceDefinitions().List(context.TODO(), metav1.ListOptions{
		FieldSelector: "metadata.name=certificates.cert-manager.io",
	})
	if err != nil {
		log.Log.Error(err, "unable to fetch CustomResourceDefinitions")
		return ctrl.Result{}, err
	}
	if len(crdList.Items) == 0 {
		// cert-manager is not installed
		log.Log.Info("cert-manager is not installed")
		return ctrl.Result{}, nil
	}

	customCert := &proxmoxv1alpha1.CustomCertificate{}
	if err := r.Get(ctx, req.NamespacedName, customCert); err != nil {
		log.Log.Error(err, "unable to fetch CustomCertificate")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// This controller implements two main features:
	// 1. It creates a certificate using cert-manager and stores it in a secret
	// 2. It updates the Proxmox node with the new certificate

	// Create a certificate using cert-manager
	// 1. Create a Certificate resource
	certManagerSpec := customCert.Spec.CertManagerSpec
	commonName := certManagerSpec.CommonName
	issuerRef := certManagerSpec.IssuerRef
	secretName := certManagerSpec.SecretName
	usages := certManagerSpec.Usages

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CustomCertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.CustomCertificate{}).
		Complete(r)
}
