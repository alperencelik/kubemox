package proxmox

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

// Credentials are the resolved password and API token secret of a connection,
// with the Secrets they were read from.
type Credentials struct {
	Password string
	Secret   string
	// ObservedSecrets carries the resourceVersion of every Secret read while
	// resolving, so the controller can publish what it saw in the status.
	ObservedSecrets []proxmoxv1alpha1.ObservedSecret
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
	password, passwordFrom, err := resolveCredential(ctx, r, "password", spec.Password,
		"passwordFrom", spec.PasswordFrom)
	if err != nil {
		return Credentials{}, err
	}
	secret, secretFrom, err := resolveCredential(ctx, r, "secret", spec.Secret, "secretFrom", spec.SecretFrom)
	if err != nil {
		return Credentials{}, err
	}
	creds := Credentials{Password: password, Secret: secret}
	for _, observed := range []*proxmoxv1alpha1.ObservedSecret{passwordFrom, secretFrom} {
		if observed == nil || containsSecret(creds.ObservedSecrets, *observed) {
			continue
		}
		creds.ObservedSecrets = append(creds.ObservedSecrets, *observed)
	}
	return creds, nil
}

// containsSecret reports whether the list already names that Secret, so two
// references to the same one are published once.
func containsSecret(list []proxmoxv1alpha1.ObservedSecret, want proxmoxv1alpha1.ObservedSecret) bool {
	for _, got := range list {
		if got.Namespace == want.Namespace && got.Name == want.Name {
			return true
		}
	}
	return false
}

// resolveCredential resolves one credential that may be written inline or
// referenced from a Secret, and reports which Secret version it read. The CRD
// rejects specs that set both, but the check is repeated here for objects that
// were admitted by an older CRD.
func resolveCredential(ctx context.Context, r client.Reader, inlineField, inline, refField string,
	ref *proxmoxv1alpha1.SecretKeyReference) (string, *proxmoxv1alpha1.ObservedSecret, error) {
	switch {
	case inline != "" && ref != nil:
		return "", nil, fmt.Errorf("both %s and %s are set; use one", inlineField, refField)
	case ref == nil:
		return inline, nil, nil
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil, fmt.Errorf("%s: secret %s/%s not found", refField, ref.Namespace, ref.Name)
		}
		return "", nil, fmt.Errorf("%s: reading secret %s/%s: %w", refField, ref.Namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", nil, fmt.Errorf("%s: key %q not found in secret %s/%s",
			refField, ref.Key, ref.Namespace, ref.Name)
	}
	if len(value) == 0 {
		return "", nil, fmt.Errorf("%s: key %q in secret %s/%s is empty",
			refField, ref.Key, ref.Namespace, ref.Name)
	}
	// The version comes from this very read, so it can never claim a version
	// the credential above did not come from.
	return string(value), &proxmoxv1alpha1.ObservedSecret{
		Name:            secret.Name,
		Namespace:       secret.Namespace,
		ResourceVersion: secret.ResourceVersion,
	}, nil
}
