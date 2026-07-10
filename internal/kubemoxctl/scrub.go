package kubemoxctl

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Scrub removes cluster-specific metadata so the object is safe to create on a
// fresh cluster. It clears ownerReferences UIDs (names are kept — they get
// rewritten later by RewriteOwnerRefs) and drops the status subresource.
func Scrub(obj *unstructured.Unstructured) {
	for _, field := range []string{
		"resourceVersion",
		"uid",
		"generation",
		"creationTimestamp",
		"deletionTimestamp",
		"deletionGracePeriodSeconds",
		"selfLink",
		"managedFields",
	} {
		unstructured.RemoveNestedField(obj.Object, "metadata", field)
	}

	refs, found, _ := unstructured.NestedSlice(obj.Object, "metadata", "ownerReferences")
	if found {
		for i, raw := range refs {
			ref, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			delete(ref, "uid")
			refs[i] = ref
		}
		_ = unstructured.SetNestedSlice(obj.Object, refs, "metadata", "ownerReferences")
	}

	delete(obj.Object, "status")
}
