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
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	v1 "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
)

// CustomCertificateReconciler reconciles a CustomCertificate object
type CustomCertificateReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
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

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=customcertificates/finalizers,verbs=update
// +kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CustomCertificate object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile

// CustomCertifaceController implements two main features:
// 1. It creates a certificate using cert-manager and stores it in a secret
// 2. It updates the Proxmox node with the new certificate
func (r *CustomCertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	customCert := &proxmoxv1alpha1.CustomCertificate{}
	err := r.Get(ctx, req.NamespacedName, customCert)
	if err != nil {
		return ctrl.Result{}, r.handleResourceNotFound(ctx, err)
	}
	// Get the Proxmox client reference
	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, customCert.Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
	}

	// Check if the CustomCertificate resource is marked for deletion
	if customCert.DeletionTimestamp.IsZero() {
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
			res, delErr := r.handleDelete(ctx, pc, customCert)
			if delErr != nil {
				logger.Error(delErr, "unable to delete CustomCertificate")
				return res, delErr
			}
		}
		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Create or update the Certificate obj
	result, err := r.handleCertificate(ctx, pc, customCert)
	if err != nil {
		logger.Error(err, "unable to create CustomCertificate")
	}
	return result, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *CustomCertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Check if cert-manager exists
	certManagerExists, err := kubernetes.CheckCertManagerCRDsExists()
	if err != nil {
		log.Log.Error(err, "Error checking cert-manager CRDs, can't start CustomCertificate controller")
		return nil
	}
	if !certManagerExists {
		log.Log.Info("cert-manager is not installed. Please install cert-manager to use CustomCertificate controller.")
		return nil
	} else {
		log.Log.Info("cert-manager is installed")
	}

	return ctrl.NewControllerManagedBy(mgr).
		Owns(&certmanagerv1.Certificate{}).
		For(&proxmoxv1alpha1.CustomCertificate{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				condition1 := e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration()
				condition2 := e.ObjectNew.GetDeletionTimestamp().IsZero()
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

func (r *CustomCertificateReconciler) handleDelete(ctx context.Context,
	pc *proxmox.ProxmoxClient, customCert *proxmoxv1alpha1.CustomCertificate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var err error
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
	err = pc.DeleteCustomCertificate(customCert.Spec.NodeName)
	if err != nil {
		logger.Error(err, "unable to delete CustomCertificate from Proxmox")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, err
	}
	// Remove the finalizer
	logger.Info("Removing finalizer from CustomCertificate")

	controllerutil.RemoveFinalizer(customCert, customCertificateFinalizerName)
	if err = r.Update(ctx, customCert); err != nil {
		log.Log.Error(err, "Error updating CustomCertificate")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *CustomCertificateReconciler) handleCertificate(ctx context.Context,
	pc *proxmox.ProxmoxClient, customCert *proxmoxv1alpha1.CustomCertificate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// TODO: First make sure that the issuerRef exists

	// Create or update the Certificate obj
	certObj, err := r.CreateOrUpdateCertificateObject(ctx, customCert)
	if err != nil {
		logger.Error(err, "unable to create or update Certificate object")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, err
	}
	// Wait till the certificate is ready
	ready, err := kubernetes.WaitForCertificateReady(certObj)
	if err != nil {
		logger.Error(err, "unable to check Certificate whether it's ready")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, err
	}
	if !ready {
		logger.Info("Certificate is not ready yet, requeuing")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, nil
	}

	// Retrieve the certificate and private key
	tlsCrt, tlsKey, err := kubernetes.GetCertificateSecretKeys(certObj)
	if err != nil {
		logger.Error(err, "unable to get certificate and private key")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, err
	}
	// Update the CustomCertificate resource
	customCert.Spec.ProxmoxCertSpec.Certificate = string(tlsCrt)
	customCert.Spec.ProxmoxCertSpec.PrivateKey = string(tlsKey)
	if err = r.Update(ctx, customCert); err != nil {
		logger.Error(err, "unable to update CustomCertificate")
		return ctrl.Result{}, err
	}
	// TODO: Diff the key in Proxmox and the key in the secret
	// Upload the certificate to the Proxmox node
	if err = pc.CreateCustomCertificate(customCert.Spec.NodeName, &customCert.Spec.ProxmoxCertSpec); err != nil {
		logger.Error(err, "unable to create CustomCertificate in Proxmox")
		return ctrl.Result{Requeue: true, RequeueAfter: CustomCertReconcilationPeriod * time.Second}, err
	}
	// Update the condition for the CustomCertificate
	if !meta.IsStatusConditionPresentAndEqual(customCert.Status.Conditions, typeAvailableCustomCertificate, metav1.ConditionTrue) {
		meta.SetStatusCondition(&customCert.Status.Conditions, metav1.Condition{
			Type:    typeAvailableCustomCertificate,
			Status:  metav1.ConditionTrue,
			Reason:  "Available",
			Message: "CustomCertificate is available",
		})
		if err = r.Status().Update(ctx, customCert); err != nil {
			logger.Error(err, "Error updating CustomCertificate status")
			return ctrl.Result{Requeue: true}, client.IgnoreNotFound(err)
		}
	}
	logger.Info("CustomCertificate is successfully created/updated")

	return ctrl.Result{}, nil
}

func (r *CustomCertificateReconciler) CreateOrUpdateCertificateObject(ctx context.Context, customCertificate *proxmoxv1alpha1.CustomCertificate) (certObj *certmanagerv1.Certificate, err error) {

	cert := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      customCertificate.Name,
			Namespace: os.Getenv("POD_NAMESPACE"), // Create cert object in the same namespace as the operator
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cert, func() error {
		// Set the owner ref
		if err := controllerutil.SetOwnerReference(customCertificate, cert, r.Scheme); err != nil {
			return err
		}
		usages := make([]certmanagerv1.KeyUsage, len(customCertificate.Spec.CertManagerSpec.Usages))
		for i, usage := range customCertificate.Spec.CertManagerSpec.Usages {
			usages[i] = certmanagerv1.KeyUsage(usage)
		}
		cert.Spec = certmanagerv1.CertificateSpec{
			CommonName: customCertificate.Spec.CertManagerSpec.CommonName,
			DNSNames:   customCertificate.Spec.CertManagerSpec.DNSNames,
			IssuerRef: v1.ObjectReference{
				Name:  customCertificate.Spec.CertManagerSpec.IssuerRef.Name,
				Kind:  customCertificate.Spec.CertManagerSpec.IssuerRef.Kind,
				Group: customCertificate.Spec.CertManagerSpec.IssuerRef.Group,
			},
			SecretName: customCertificate.Spec.CertManagerSpec.SecretName,
			Usages:     usages,
		}
		return nil
	},
	)
	if err != nil {
		return nil, err
	}
	return cert, nil
}
