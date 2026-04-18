package kubemoxctl

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OwnerKey identifies a potential parent resource. Namespace is empty for
// cluster-scoped kinds.
type OwnerKey struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// UIDMap holds the UID each imported resource received on the target cluster.
// Children created later consult this map to rewrite their ownerReferences.
type UIDMap map[OwnerKey]string

// Record stores the new UID assigned by the target cluster for an imported
// object so subsequent children can point at it.
func (m UIDMap) Record(obj *unstructured.Unstructured) {
	m[OwnerKey{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
	}] = string(obj.GetUID())
}

// RewriteOwnerRefs replaces the UID on each ownerReference with the UID the
// parent received on the target cluster. Refs whose parent is missing from the
// map are dropped — the tool only manages kubemox kinds, and dangling refs
// would block creation.
func RewriteOwnerRefs(obj *unstructured.Unstructured, uids UIDMap) {
	refs := obj.GetOwnerReferences()
	if len(refs) == 0 {
		return
	}
	kept := make([]metav1.OwnerReference, 0, len(refs))
	for _, ref := range refs {
		key := OwnerKey{
			APIVersion: ref.APIVersion,
			Kind:       ref.Kind,
			Namespace:  obj.GetNamespace(),
			Name:       ref.Name,
		}
		newUID, ok := uids[key]
		if !ok {
			// Cluster-scoped parent: retry with empty namespace.
			key.Namespace = ""
			newUID, ok = uids[key]
		}
		if !ok {
			continue
		}
		ref.UID = typeUID(newUID)
		kept = append(kept, ref)
	}
	obj.SetOwnerReferences(kept)
}
