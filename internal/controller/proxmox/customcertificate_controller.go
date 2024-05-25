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
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// CustomCertificateReconciler reconciles a CustomCertificate object
type CustomCertificateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	customCertificateFinalizerName    = "customcertificate.proxmox.alperen.cloud/finalizer"
	CustomCertReconcilationPeriod     = 10
	CustomCertMaxConcurrentReconciles = 10

	// Status conditions
	typeAvailableCustomCertificate = "Available"
	typeCreatingCustomCertificate  = "Creating"
	typeDeletingCustomCertificate  = "Deleting"
	typeErrorCustomCertificate     = "Error"
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/finalizers,verbs=update
//+kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list;watch

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
	logger := log.FromContext(ctx)

	customCert := &proxmoxv1alpha1.CustomCertificate{}
	err := r.Get(ctx, req.NamespacedName, customCert)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}

	// Check if the CustomCertificate resource is marked for deletion
	if customCert.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(customCert, customCertificateFinalizerName) {
			controllerutil.AddFinalizer(customCert, customCertificateFinalizerName)
			if err = r.Update(ctx, customCert); err != nil {
				log.Log.Error(err, "Error updating CustomCertificate")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(customCert, customCertificateFinalizerName) {
			// Delete the custom certificate
			logger.Info("Deleting the CustomCertificate")

			// Update the condition for the CustomCertificate if it is being deleted
			if !meta.IsStatusConditionPresentAndEqual(customCert.Status.Conditions, typeDeletingCustomCertificate, metav1.ConditionTrue) {
				meta.SetStatusCondition(&customCert.Status.Conditions, metav1.Condition{
					Type:    typeDeletingCustomCertificate,
					Status:  metav1.ConditionTrue,
					Reason:  "Deleting",
					Message: "Deleting CustomCertificate",
				})
				if err = r.Status().Update(ctx, customCert); err != nil {
					logger.Error(err, "Error updating CustomCertificate status")
					return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
				}
			} else {
				return ctrl.Result{}, nil
			}
			// Delete the custom certificate from Proxmox
			proxmox.DeleteCustomCertificate(customCert.Spec.NodeName)
			// Remove the finalizer
			logger.Info("Removing finalizer from CustomCertificate")

			controllerutil.RemoveFinalizer(customCert, customCertificateFinalizerName)
			if err = r.Update(ctx, customCert); err != nil {
				log.Log.Error(err, "Error updating CustomCertificate")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// This controller implements two main features:
	// 1. It creates a certificate using cert-manager and stores it in a secret
	// 2. It updates the Proxmox node with the new certificate

	// Create a certificate using cert-manager
	// 1. Create a Certificate resource
	// Check if the Certificate resource exists
	certExists := kubernetes.CheckCertificateExists(customCert.Name, customCert.Namespace)
	if !certExists {
		// Create a Certificate resource
		logger.Info("Creating a Certificate resource", "Certificate", customCert.Spec.CertManagerSpec)
		cert, certErr := kubernetes.CreateCertificate(customCert)
		if certErr != nil {
			logger.Error(certErr, "unable to create Certificate resource")
			return ctrl.Result{}, err
		}
		logger.Info("Certificate is created", "Certificate", cert)
		// Request the request to update the proxmoxCertSpec with the new certificate
		return ctrl.Result{Requeue: true}, nil
	} else {
		// Get the Certificate resource
		certManagerCertificate := kubernetes.GetCertificate(customCert)
		kubernetes.UpdateCertificate(&customCert.Spec.CertManagerSpec, certManagerCertificate)
		// Retrieve the certificate and private key
		tlsCrt, tlsKey := kubernetes.GetCertificateSecretKeys(certManagerCertificate)
		// Update the CustomCertificate resource
		customCert.Spec.ProxmoxCertSpec.Certificate = string(tlsCrt)
		customCert.Spec.ProxmoxCertSpec.PrivateKey = string(tlsKey)
		if err = r.Update(ctx, customCert); err != nil {
			logger.Error(err, "unable to update CustomCertificate")
			return ctrl.Result{}, err
		}
		// TODO: Diff the key in Proxmox and the key in the secret
		// Upload the certificate to the Proxmox node
		if err = proxmox.CreateCustomCertificate(customCert.Spec.NodeName, &customCert.Spec.ProxmoxCertSpec); err != nil {
			logger.Error(err, "unable to create CustomCertificate in Proxmox")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CustomCertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Check if cert-manager exists
	certManagerExists := kubernetes.CheckCertManagerCRDsExists()
	if !certManagerExists {
		log.Log.Info("cert-manager is not installed. Please install cert-manager to use CustomCertificate controller.")
		return nil
	} else {
		log.Log.Info("cert-manager is installed")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.CustomCertificate{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldCustomCert := e.ObjectOld.(*proxmoxv1alpha1.CustomCertificate)
				newCustomCert := e.ObjectNew.(*proxmoxv1alpha1.CustomCertificate)
				condition1 := !reflect.DeepEqual(oldCustomCert.Spec, newCustomCert.Spec)
				condition2 := newCustomCert.ObjectMeta.GetDeletionTimestamp().IsZero()
				return condition1 || !condition2
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: CustomCertMaxConcurrentReconciles}).
		Complete(r)
}

func (r *CustomCertificateReconciler) handleResourceNotFound(ctx context.Context, err error) error {
	logger := log.FromContext(ctx)
	if errors.IsNotFound(err) {
		logger.Info("CustomCertificate resource not found. Ignoring since object must be deleted")
		return nil
	}
	logger.Error(err, "Failed to get CustomCertificate")
	return err
}
