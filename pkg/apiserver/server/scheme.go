package server

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
)

// Scheme is the runtime scheme served by the aggregated apiserver.
// All ops API group versions register themselves here at init.
var (
	Scheme = runtime.NewScheme()
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	utilruntime.Must(pveopsv1alpha1.AddToScheme(Scheme))
	// APIInstaller looks up ListOptions/CreateOptions/PatchOptions/etc.
	// at the GVK returned by APIGroupInfo.OptionsExternalVersion, which
	// NewDefaultAPIGroupInfo sets to {Group:"", Version:"v1"} — the core
	// meta v1 group, NOT our own group's v1alpha1. So register meta/v1
	// kinds against that GV.
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
}
