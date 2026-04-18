package kubemoxctl

import (
	"bytes"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// WriteArchive serializes a slice of unstructured objects as a multi-document
// YAML stream (each object preceded by `---`). No header or wrapper is added —
// the output is plain kubectl-style manifests.
func WriteArchive(w io.Writer, objs []*unstructured.Unstructured) error {
	for _, obj := range objs {
		data, err := yaml.Marshal(obj.Object)
		if err != nil {
			return fmt.Errorf("marshal %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		if _, err := fmt.Fprintln(w, "---"); err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// ReadArchive parses a multi-document YAML stream into unstructured objects.
// Empty documents are skipped silently.
func ReadArchive(r io.Reader) ([]*unstructured.Unstructured, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(buf), 4096)
	var out []*unstructured.Unstructured
	for {
		raw := map[string]any{}
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, fmt.Errorf("decode document: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: raw})
	}
}

// metaPatchOpts returns the default PatchOptions used when updating
// annotations. Kept in this file so the library has a single place to tweak
// field-manager metadata later.
func metaPatchOpts() metav1.PatchOptions {
	return metav1.PatchOptions{FieldManager: "kubemoxctl"}
}

// typeUID converts a string to types.UID. Exists to avoid importing types in
// ownerref.go just for the cast.
func typeUID(s string) types.UID { return types.UID(s) }
