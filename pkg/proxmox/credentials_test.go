package proxmox

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// Ці рядки повторюються в тестах пакета достатньо, щоб goconst рахував їх
// дубльованими літералами. Імена з префіксом test — пакет спільний із
// тестами інших гілок форку.
const (
	testTokenID  = "root@pam!kubemox"
	testUser     = "root@pam"
	testPassword = "password"
	testPVEURL   = "https://pve:8006"
	testLocalURL = "https://localhost:8006"
)

const (
	credNamespace  = "kubemox-system"
	credSecretName = "proxmox-credentials"
	// credSecretValue is a fixture, and doubles as a canary: no error message
	// may ever contain it.
	credSecretValue = "do-not-leak-this-value" //nolint:gosec // test fixture, not a credential
)

func credentialsClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func credentialsSecret(data map[string]string) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: credSecretName, Namespace: credNamespace},
		Data:       map[string][]byte{},
	}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func secretKeyRef(key string) *proxmoxv1alpha1.SecretKeyReference {
	return &proxmoxv1alpha1.SecretKeyReference{Name: credSecretName, Namespace: credNamespace, Key: key}
}

func assertNoLeak(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), credSecretValue) {
		t.Fatal("error message leaks a value read from the Secret")
	}
}

// TestResolveCredentials_PasswordFromSecret is the point of passwordFrom: the
// password stored in the Secret must reach the credentials.
func TestResolveCredentials_PasswordFromSecret(t *testing.T) {
	cl := credentialsClient(t, credentialsSecret(map[string]string{testPassword: credSecretValue}))
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, Username: testUser, PasswordFrom: secretKeyRef(testPassword),
	}

	got, err := ResolveCredentials(context.Background(), cl, spec)
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if got.Password != credSecretValue {
		t.Fatal("Password does not match the value stored in the Secret")
	}
}

// TestResolveCredentials_TokenSecretFromSecret does the same for API tokens.
func TestResolveCredentials_TokenSecretFromSecret(t *testing.T) {
	cl := credentialsClient(t, credentialsSecret(map[string]string{"token": credSecretValue}))
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, TokenID: testTokenID, SecretFrom: secretKeyRef("token"),
	}

	got, err := ResolveCredentials(context.Background(), cl, spec)
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if got.Secret != credSecretValue {
		t.Fatal("Secret does not match the value stored in the Secret")
	}
}

// TestResolveCredentials_InlineOnly pins that connections written the old way
// keep working unchanged: no Secret is read and the inline values pass through.
func TestResolveCredentials_InlineOnly(t *testing.T) {
	cl := credentialsClient(t)
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, TokenID: testTokenID, Secret: "inline-token",
	}

	got, err := ResolveCredentials(context.Background(), cl, spec)
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if got.Secret != "inline-token" {
		t.Fatalf("Secret = %q, want the inline value", got.Secret)
	}
}

// TestResolveCredentials_MissingSecret must fail loudly and name what is
// missing. Silently falling back to empty credentials would turn a typo in a
// Secret name into a confusing authentication error against Proxmox.
func TestResolveCredentials_MissingSecret(t *testing.T) {
	cl := credentialsClient(t)
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, TokenID: testTokenID, SecretFrom: secretKeyRef("token"),
	}

	_, err := ResolveCredentials(context.Background(), cl, spec)
	if err == nil {
		t.Fatal("expected an error for a Secret that does not exist")
	}
	if !strings.Contains(err.Error(), credNamespace+"/"+credSecretName) {
		t.Fatalf("error %q does not name the missing Secret", err)
	}
}

// TestResolveCredentials_MissingKey names the key, and must not echo any of the
// values that are present in the Secret.
func TestResolveCredentials_MissingKey(t *testing.T) {
	cl := credentialsClient(t, credentialsSecret(map[string]string{"other": credSecretValue}))
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, TokenID: testTokenID, SecretFrom: secretKeyRef("token"),
	}

	_, err := ResolveCredentials(context.Background(), cl, spec)
	if err == nil {
		t.Fatal("expected an error for a key that is not in the Secret")
	}
	if !strings.Contains(err.Error(), `"token"`) {
		t.Fatalf("error %q does not name the missing key", err)
	}
	assertNoLeak(t, err)
}

// TestResolveCredentials_EmptyValue treats an empty value as missing: an empty
// password is never a working credential.
func TestResolveCredentials_EmptyValue(t *testing.T) {
	cl := credentialsClient(t, credentialsSecret(map[string]string{testPassword: ""}))
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, Username: testUser, PasswordFrom: secretKeyRef(testPassword),
	}

	_, err := ResolveCredentials(context.Background(), cl, spec)
	if err == nil {
		t.Fatal("expected an error for an empty value in the Secret")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error %q does not say the value is empty", err)
	}
}

// TestResolveCredentials_InlineAndReferenceBoth is rejected by the CRD's CEL
// rule too; this is the same guarantee for objects that reach the resolver
// some other way, such as a CRD installed from an older chart.
func TestResolveCredentials_InlineAndReferenceBoth(t *testing.T) {
	cl := credentialsClient(t, credentialsSecret(map[string]string{testPassword: credSecretValue}))
	spec := &proxmoxv1alpha1.ProxmoxConnectionSpec{
		Endpoint: testPVEURL, Username: testUser,
		Password: "inline", PasswordFrom: secretKeyRef(testPassword),
	}

	_, err := ResolveCredentials(context.Background(), cl, spec)
	if err == nil {
		t.Fatal("expected an error when both password and passwordFrom are set")
	}
	if !strings.Contains(err.Error(), "passwordFrom") {
		t.Fatalf("error %q does not name the conflicting fields", err)
	}
	assertNoLeak(t, err)
}
