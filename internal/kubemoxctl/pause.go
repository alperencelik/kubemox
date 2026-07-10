package kubemoxctl

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// Mirrors the values in pkg/kubernetes/constants.go. Duplicated here so the
// tool has no import cycle with controller code.
const (
	ReconcileModeAnnotation = "proxmox.alperen.cloud/reconcile-mode"
	ReconcileModeDisable    = "Disable"
)

// SetPausedAnnotation stamps the Disable reconcile-mode annotation on an
// in-memory object. Used during import so the resource is created paused.
func SetPausedAnnotation(obj *unstructured.Unstructured) {
	anns := obj.GetAnnotations()
	if anns == nil {
		anns = map[string]string{}
	}
	anns[ReconcileModeAnnotation] = ReconcileModeDisable
	obj.SetAnnotations(anns)
}

// PatchPausedAnnotation adds the Disable annotation to a live resource via a
// JSON merge patch. Used by `export --pause-source` on the source cluster.
func PatchPausedAnnotation(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) error {
	return patchAnnotation(ctx, dyn, gvr, namespace, name, ReconcileModeDisable)
}

// PatchRemovePausedAnnotation clears the reconcile-mode annotation on a live
// resource. Used by `resume` on the target cluster.
func PatchRemovePausedAnnotation(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) error {
	return patchAnnotation(ctx, dyn, gvr, namespace, name, nil)
}

func patchAnnotation(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, value any) error {
	payload := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				ReconcileModeAnnotation: value,
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal annotation patch: %w", err)
	}
	var ri dynamic.ResourceInterface = dyn.Resource(gvr)
	if namespace != "" {
		ri = dyn.Resource(gvr).Namespace(namespace)
	}
	if _, err := ri.Patch(ctx, name, types.MergePatchType, data, metaPatchOpts()); err != nil {
		return fmt.Errorf("patch %s/%s: %w", gvr.Resource, name, err)
	}
	return nil
}
