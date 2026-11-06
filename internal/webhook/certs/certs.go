package certs

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	certValidity    = 10 * 365 * 24 * time.Hour
	certRenewBefore = 30 * 24 * time.Hour
)

// SelfSigned serves the webhook with a certificate the operator issues
// itself, for clusters without cert-manager.
func SelfSigned(ctx context.Context, c client.Client,
	namespace, secretName, serviceName, webhookConfigName string) (func(*tls.Config), error) {
	secret, err := ensureCertSecret(ctx, c, namespace, secretName, serviceName)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return nil, fmt.Errorf("loading webhook certificate: %w", err)
	}
	if err := injectCABundle(ctx, c, webhookConfigName, secret.Data["ca.crt"]); err != nil {
		return nil, err
	}
	return func(cfg *tls.Config) {
		cfg.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &pair, nil }
	}, nil
}

// ensureCertSecret returns the Secret holding a valid chain, issuing a new one
// when it is missing or about to expire.
func ensureCertSecret(ctx context.Context, c client.Client,
	namespace, secretName, serviceName string) (*corev1.Secret, error) {
	for range 3 {
		secret := &corev1.Secret{}
		err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, secret)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("reading webhook cert secret: %w", err)
		}
		exists := err == nil
		if exists && certUsable(secret.Data[corev1.TLSCertKey]) {
			return secret, nil
		}

		data, err := issueCerts(serviceName, namespace)
		if err != nil {
			return nil, err
		}
		secret.Name, secret.Namespace = secretName, namespace
		secret.Type = corev1.SecretTypeTLS
		secret.Data = data
		if exists {
			err = c.Update(ctx, secret)
		} else {
			err = c.Create(ctx, secret)
		}
		if err == nil {
			return secret, nil
		}
		if !apierrors.IsAlreadyExists(err) && !apierrors.IsConflict(err) {
			return nil, fmt.Errorf("writing webhook cert secret: %w", err)
		}
	}
	return nil, errors.New("webhook cert secret kept changing while issuing a certificate")
}

func certUsable(certPEM []byte) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	return err == nil && time.Now().Add(certRenewBefore).Before(cert.NotAfter)
}

// issueCerts creates a CA and a serving certificate for the webhook Service,
// returned as Secret data. The CA key is not kept: both are re-issued together.
func issueCerts(serviceName, namespace string) (map[string][]byte, error) {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: "kubemox-webhook-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	host := fmt.Sprintf("%s.%s.svc", serviceName, namespace)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano() + 1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host, host + ".cluster.local"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"ca.crt":                pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		corev1.TLSCertKey:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		corev1.TLSPrivateKeyKey: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

func injectCABundle(ctx context.Context, c client.Client, name string, caPEM []byte) error {
	cfg := &admissionregistrationv1.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Get(ctx, client.ObjectKeyFromObject(cfg), cfg); err != nil {
		return fmt.Errorf("reading ValidatingWebhookConfiguration %q: %w", name, err)
	}
	patch := client.MergeFrom(cfg.DeepCopy())
	for i := range cfg.Webhooks {
		cfg.Webhooks[i].ClientConfig.CABundle = caPEM
	}
	if err := c.Patch(ctx, cfg, patch); err != nil {
		return fmt.Errorf("injecting caBundle into %q: %w", name, err)
	}
	return nil
}
