package certs

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSelfSigned(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "kubemox-validating-webhook"},
		Webhooks:   []admissionregistrationv1.ValidatingWebhook{{Name: "vvirtualmachine.kb.io"}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vwc).Build()
	ctx := context.Background()

	start := func() *tls.Certificate {
		t.Helper()
		opt, err := SelfSigned(ctx, c, "kubemox", "kubemox-webhook-cert", "kubemox-webhook", vwc.Name)
		if err != nil {
			t.Fatal(err)
		}
		cfg := &tls.Config{}
		opt(cfg)
		cert, err := cfg.GetCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}

	first := start()
	if second := start(); !bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Error("a second replica issued a new certificate instead of reusing the stored one")
	}

	// The served certificate must verify against the injected caBundle for the
	// Service's DNS name, which is exactly what the API server checks.
	if err := c.Get(ctx, client.ObjectKeyFromObject(vwc), vwc); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(vwc.Webhooks[0].ClientConfig.CABundle) {
		t.Fatal("caBundle was not injected")
	}
	leaf, err := x509.ParseCertificate(first.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "kubemox-webhook.kubemox.svc", Roots: roots}); err != nil {
		t.Errorf("serving certificate does not verify against caBundle: %v", err)
	}

	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: "kubemox", Name: "kubemox-webhook-cert"}, secret); err != nil {
		t.Fatal(err)
	}
	if secret.Type != corev1.SecretTypeTLS {
		t.Errorf("secret type = %q, want %q", secret.Type, corev1.SecretTypeTLS)
	}
}
