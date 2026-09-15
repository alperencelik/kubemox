package proxmox

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// secretRefCacheTTL bounds how long a client built from Secret-backed
// credentials is reused before the Secret is read again, so a rotated
// credential is picked up without touching the ProxmoxConnection.
const secretRefCacheTTL = 5 * time.Minute

// Credentials are the resolved password and API token secret of a connection.
type Credentials struct {
	Password string
	Secret   string
}

// usesSecretRef reports whether any credential of the spec is read from a Secret.
func usesSecretRef(spec *proxmoxv1alpha1.ProxmoxConnectionSpec) bool {
	return spec.PasswordFrom != nil || spec.SecretFrom != nil
}

// ResolveCredentials returns the password and API token secret of a
// connection, reading them from Secrets where the spec references one and
// passing inline values through unchanged.
//
// Secrets are read through the given reader. In the operator that is the
// manager's client with Secrets excluded from its cache, so each read is a
// single GET by name and never starts an informer over every Secret in the
// cluster.
//
// Errors name the field, namespace, Secret and key involved - never a value.
func ResolveCredentials(ctx context.Context, r client.Reader,
	spec *proxmoxv1alpha1.ProxmoxConnectionSpec) (Credentials, error) {
	password, err := resolveCredential(ctx, r, "password", spec.Password, "passwordFrom", spec.PasswordFrom)
	if err != nil {
		return Credentials{}, err
	}
	secret, err := resolveCredential(ctx, r, "secret", spec.Secret, "secretFrom", spec.SecretFrom)
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{Password: password, Secret: secret}, nil
}

// resolveCredential resolves one credential that may be written inline or
// referenced from a Secret. The CRD rejects specs that set both, but the check
// is repeated here for objects that were admitted by an older CRD.
func resolveCredential(ctx context.Context, r client.Reader, inlineField, inline, refField string,
	ref *proxmoxv1alpha1.SecretKeyReference) (string, error) {
	switch {
	case inline != "" && ref != nil:
		return "", fmt.Errorf("both %s and %s are set; use one", inlineField, refField)
	case ref == nil:
		return inline, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("%s: secret %s/%s not found", refField, ref.Namespace, ref.Name)
		}
		return "", fmt.Errorf("%s: reading secret %s/%s: %w", refField, ref.Namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("%s: key %q not found in secret %s/%s", refField, ref.Key, ref.Namespace, ref.Name)
	}
	if len(value) == 0 {
		return "", fmt.Errorf("%s: key %q in secret %s/%s is empty", refField, ref.Key, ref.Namespace, ref.Name)
	}
	return string(value), nil
}
