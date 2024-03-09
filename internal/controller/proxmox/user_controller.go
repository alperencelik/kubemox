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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// UserReconciler reconciles a User object
type UserReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	userFinalizer = "user.proxmox.alperen.cloud/finalizer"
)

//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=users,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=users/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=users/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the User object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *UserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get the User object
	user := &proxmoxv1alpha1.User{}
	err := r.Get(ctx, req.NamespacedName, user)
	if err != nil {
		logger.Error(err, "unable to fetch User")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the User object is marked for deletion, which is indicated by the deletion timestamp
	if user.ObjectMeta.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(user, userFinalizer) {
			controllerutil.AddFinalizer(user, userFinalizer)
			if err := r.Update(ctx, user); err != nil {
				logger.Error(err, "unable to update User")
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(user, userFinalizer) {
			// Custom delete logic here
			proxmox.DeleteUser(user.Spec.UserID)
			controllerutil.RemoveFinalizer(user, userFinalizer)
			if err := r.Update(ctx, user); err != nil {
				logger.Error(err, "unable to update User")
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Check if the User exists
	userExists, err := proxmox.UserExists(user.Spec.UserID)
	if err != nil {
		logger.Error(err, "unable to check if user exists")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	resourceKey := fmt.Sprintf("%s/%s", user.Namespace, user.Name)
	if userExists {
		// User exists, do nothing
	} else {
		logger.Info("User does not exist, creating user")
		if isProcessed(resourceKey) {
			return ctrl.Result{}, nil
		} else {
			err = proxmox.CreateUser(user)
			if err != nil {
				logger.Error(err, "unable to create user")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			processedResources[resourceKey] = true
		}
	}

	// Check if the user password is empty
	if user.Spec.Password == "" {
		// Update the user spec with the generated password
		password, err := proxmox.UpdatePassword(user)
		if err != nil {
			logger.Error(err, "unable to update user password")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		err = r.UpdatePasswordField(ctx, user, password)
		if err != nil {
			logger.Error(err, "unable to update user password")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, client.IgnoreNotFound(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.User{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(&UserReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}

func (r *UserReconciler) UpdatePasswordField(ctx context.Context, user *proxmoxv1alpha1.User, password string) error {
	// Update the user spec with the generated password
	user.Spec.Password = password
	err := r.Update(ctx, user)
	if err != nil {
		return err
	}
	return nil
}
