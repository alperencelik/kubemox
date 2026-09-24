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
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// goconst counts identical literals per package; these three appear in every
// case that carries credentials.
const (
	testConnUser   = "root@pam"
	testConnToken  = "root@pam!t"
	testConnSecret = "password"
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
			proxmoxv1alpha1.ProxmoxConnectionSpec{Username: testConnUser, Password: "p"}, true),
		Entry("username with passwordFrom", "v-user-passwordfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{Username: testConnUser, PasswordFrom: secretRef(testConnSecret)}, true),
		Entry("tokenID with secret", "v-token-secret",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: testConnToken, Secret: "s"}, true),
		Entry("tokenID with secretFrom", "v-token-secretfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: testConnToken, SecretFrom: secretRef("token")}, true),

		Entry("password and passwordFrom together", "x-password-and-passwordfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				Username: testConnUser, Password: "p", PasswordFrom: secretRef(testConnSecret),
			}, false),
		Entry("secret and secretFrom together", "x-secret-and-secretfrom",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				TokenID: testConnToken, Secret: "s", SecretFrom: secretRef("token"),
			}, false),
		Entry("tokenID with neither secret nor secretFrom", "x-token-only",
			proxmoxv1alpha1.ProxmoxConnectionSpec{TokenID: testConnToken}, false),
		Entry("passwordFrom without a username", "x-passwordfrom-no-user",
			proxmoxv1alpha1.ProxmoxConnectionSpec{PasswordFrom: secretRef(testConnSecret)}, false),
		Entry("both authentication methods at once", "x-both-methods",
			proxmoxv1alpha1.ProxmoxConnectionSpec{
				Username: testConnUser, PasswordFrom: secretRef(testConnSecret),
				TokenID: testConnToken, SecretFrom: secretRef("token"),
			}, false),
	)
})

var _ = Describe("ProxmoxConnection Secret rotation", func() {
	ctx := context.Background()

	const (
		connName   = "rotation-conn"
		secretName = "rotation-credentials"
		secretNS   = "default"
		secretKey  = "token"
	)
	connKey := types.NamespacedName{Name: connName}
	secretKeyName := types.NamespacedName{Name: secretName, Namespace: secretNS}

	var (
		server     *httptest.Server
		reconciler *ProxmoxConnectionReconciler
	)

	// A Proxmox that answers /version is enough: the controller only asks for
	// the version, and the point of these cases is what it writes afterwards.
	BeforeEach(func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"version":"8.4.1","release":"8.4","repoid":"test"}}`))
		}))

		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: secretNS},
			Data:       map[string][]byte{secretKey: []byte("first-token")},
		})).To(Succeed())

		Expect(k8sClient.Create(ctx, &proxmoxv1alpha1.ProxmoxConnection{
			ObjectMeta: metav1.ObjectMeta{Name: connName},
			Spec: proxmoxv1alpha1.ProxmoxConnectionSpec{
				Endpoint: server.URL + "/api2/json",
				TokenID:  testConnToken,
				SecretFrom: &proxmoxv1alpha1.SecretKeyReference{
					Name: secretName, Namespace: secretNS, Key: secretKey,
				},
			},
		})).To(Succeed())

		reconciler = &ProxmoxConnectionReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	AfterEach(func() {
		server.Close()
		conn := &proxmoxv1alpha1.ProxmoxConnection{}
		Expect(k8sClient.Get(ctx, connKey, conn)).To(Succeed())
		Expect(k8sClient.Delete(ctx, conn)).To(Succeed())
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, secretKeyName, secret)).To(Succeed())
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
	})

	reconcileOnce := func() reconcile.Result {
		GinkgoHelper()
		res, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: connKey})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	observedSecrets := func() []proxmoxv1alpha1.ObservedSecret {
		GinkgoHelper()
		conn := &proxmoxv1alpha1.ProxmoxConnection{}
		Expect(k8sClient.Get(ctx, connKey, conn)).To(Succeed())
		return conn.Status.ObservedSecrets
	}

	secretResourceVersion := func() string {
		GinkgoHelper()
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, secretKeyName, secret)).To(Succeed())
		return secret.ResourceVersion
	}

	It("publishes the resourceVersion of the Secret it read, and asks to be woken again", func() {
		res := reconcileOnce()

		// Nothing else wakes this controller: its event filter passes only spec
		// changes, so without the requeue a rotated Secret is never noticed. The
		// interval is written out here rather than taken from the constant - a
		// comparison against the constant would hold whatever it is changed to.
		Expect(res.RequeueAfter).To(Equal(time.Minute))
		Expect(observedSecrets()).To(Equal([]proxmoxv1alpha1.ObservedSecret{{
			Name: secretName, Namespace: secretNS, ResourceVersion: secretResourceVersion(),
		}}))

		conn := &proxmoxv1alpha1.ProxmoxConnection{}
		Expect(k8sClient.Get(ctx, connKey, conn)).To(Succeed())
		Expect(conn.Status.Version).To(Equal("8.4.1"))
	})

	It("moves the published version when the Secret is rotated", func() {
		reconcileOnce()
		before := observedSecrets()

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, secretKeyName, secret)).To(Succeed())
		secret.Data[secretKey] = []byte("rotated-token")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		reconcileOnce()
		after := observedSecrets()
		Expect(after).To(HaveLen(1))
		Expect(after[0].ResourceVersion).To(Equal(secretResourceVersion()))
		Expect(after[0].ResourceVersion).NotTo(Equal(before[0].ResourceVersion))
	})

	It("writes nothing when neither the Secret nor the connection changed", func() {
		reconcileOnce()
		conn := &proxmoxv1alpha1.ProxmoxConnection{}
		Expect(k8sClient.Get(ctx, connKey, conn)).To(Succeed())
		settled := conn.ResourceVersion

		// A minute apart forever, so a status write on every pass would be a
		// write to etcd every minute per connection - and would rebuild every
		// cached Proxmox client with it.
		reconcileOnce()
		reconcileOnce()
		Expect(k8sClient.Get(ctx, connKey, conn)).To(Succeed())
		Expect(conn.ResourceVersion).To(Equal(settled))
	})
})
