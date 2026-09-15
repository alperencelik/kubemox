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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

var _ = Describe("ProxmoxConnection Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		proxmoxconnection := &proxmoxv1alpha1.ProxmoxConnection{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ProxmoxConnection")
			err := k8sClient.Get(ctx, typeNamespacedName, proxmoxconnection)
			if err != nil && errors.IsNotFound(err) {
				resource := &proxmoxv1alpha1.ProxmoxConnection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
						Endpoint: "https://proxmox.example.com:8006",
						TokenID:  "test@pam!token",
						Secret:   "test-secret",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &proxmoxv1alpha1.ProxmoxConnection{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ProxmoxConnection")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ProxmoxConnectionReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).To(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})

var _ = Describe("ProxmoxConnection credential validation", func() {
	ctx := context.Background()
	secretRef := func(key string) *proxmoxv1alpha1.SecretKeyReference {
		return &proxmoxv1alpha1.SecretKeyReference{
			Name: "proxmox-credentials", Namespace: "kubemox-system", Key: key,
		}
	}

	// These run against the CRD envtest loads from config/crd/bases, so they
	// exercise the real CEL rule in a real API server - not a copy of it.
	DescribeTable("the CRD admits exactly one source for each credential",
		func(name string, spec proxmoxv1alpha1.ProxmoxConnectionSpec, admitted bool) {
			spec.Endpoint = "https://proxmox.example.com:8006"
			conn := &proxmoxv1alpha1.ProxmoxConnection{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       spec,
			}
			err := k8sClient.Create(ctx, conn)
			if admitted {
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, conn)).To(Succeed())
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		},
		Entry("username with password", "v-user-password",
			proxmoxv1alpha1.ProxmoxConnectionSpec{Username: "root@pam", Password: "p"}, true),
		Entry("username with passwordFrom", "v-user-passwordfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{Username: "root@pam", PasswordFrom: secretRef("password")}, true),
		Entry("tokenID with secret", "v-token-secret",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: "root@pam!t", Secret: "s"}, true),
		Entry("tokenID with secretFrom", "v-token-secretfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: "root@pam!t", SecretFrom: secretRef("token")}, true),

		Entry("password and passwordFrom together", "x-password-and-passwordfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				Username: "root@pam", Password: "p", PasswordFrom: secretRef("password"),
			}, false),
		Entry("secret and secretFrom together", "x-secret-and-secretfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				TokenID: "root@pam!t", Secret: "s", SecretFrom: secretRef("token"),
			}, false),
		Entry("tokenID with neither secret nor secretFrom", "x-token-only",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: "root@pam!t"}, false),
		Entry("passwordFrom without a username", "x-passwordfrom-no-user",
			proxmoxv1alpha1.ProxmoxConnectionSpec{PasswordFrom: secretRef("password")}, false),
		Entry("both authentication methods at once", "x-both-methods",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				Username: "root@pam", PasswordFrom: secretRef("password"),
				TokenID: "root@pam!t", SecretFrom: secretRef("token"),
			}, false),
	)
})
