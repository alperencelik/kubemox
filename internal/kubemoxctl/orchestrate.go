package kubemoxctl

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// ExportAll lists every kubemox kind and returns scrubbed copies ready to be
// serialized or replayed on a target cluster.
func ExportAll(ctx context.Context, dyn dynamic.Interface) ([]*unstructured.Unstructured, error) {
	var out []*unstructured.Unstructured
	for _, rt := range ExportOrder {
		list, err := dyn.Resource(rt.GVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", rt.Kind, err)
		}
		for i := range list.Items {
			item := list.Items[i]
			Scrub(&item)
			out = append(out, &item)
		}
	}
	return out, nil
}

// ImportAll replays objs on dyn in topological order, rewriting
// ownerReferences UIDs as parents get created. When paused is true the Disable
// reconcile-mode annotation is stamped on each object before creation.
func ImportAll(ctx context.Context, dyn dynamic.Interface, objs []*unstructured.Unstructured, paused bool) error {
	byKind := map[string][]*unstructured.Unstructured{}
	for _, obj := range objs {
		byKind[obj.GetKind()] = append(byKind[obj.GetKind()], obj)
	}
	uids := UIDMap{}
	for _, rt := range ExportOrder {
		for _, obj := range byKind[rt.Kind] {
			RewriteOwnerRefs(obj, uids)
			if paused {
				SetPausedAnnotation(obj)
			}
			created, err := dyn.Resource(rt.GVR).Namespace(obj.GetNamespace()).Create(ctx, obj, metav1.CreateOptions{FieldManager: "kubemoxctl"})
			if err != nil {
				return fmt.Errorf("create %s/%s: %w", rt.Kind, obj.GetName(), err)
			}
			uids.Record(created)
		}
	}
	return nil
}

// PauseAll stamps the Disable reconcile-mode annotation on every kubemox CR
// in the cluster reachable through dyn.
func PauseAll(ctx context.Context, dyn dynamic.Interface) error {
	for _, rt := range ExportOrder {
		list, err := dyn.Resource(rt.GVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list %s: %w", rt.Kind, err)
		}
		for i := range list.Items {
			item := &list.Items[i]
			if err := PatchPausedAnnotation(ctx, dyn, rt.GVR, item.GetNamespace(), item.GetName()); err != nil {
				return fmt.Errorf("pause %s/%s: %w", rt.Kind, item.GetName(), err)
			}
		}
	}
	return nil
}

// ResumeAll clears the Disable reconcile-mode annotation from every kubemox
// CR that has it set.
func ResumeAll(ctx context.Context, dyn dynamic.Interface) (int, error) {
	total := 0
	for _, rt := range ExportOrder {
		list, err := dyn.Resource(rt.GVR).List(ctx, metav1.ListOptions{})
		if err != nil {
			return total, fmt.Errorf("list %s: %w", rt.Kind, err)
		}
		for i := range list.Items {
			item := list.Items[i]
			if item.GetAnnotations()[ReconcileModeAnnotation] != ReconcileModeDisable {
				continue
			}
			if err := PatchRemovePausedAnnotation(ctx, dyn, rt.GVR, item.GetNamespace(), item.GetName()); err != nil {
				return total, fmt.Errorf("resume %s/%s: %w", rt.Kind, item.GetName(), err)
			}
			total++
		}
	}
	return total, nil
}
