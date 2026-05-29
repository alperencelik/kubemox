// Package registry installs the ops.proxmox.alperen.cloud API group on the
// aggregated apiserver and wires each resource (and subresource) to its
// rest.Storage implementation.
package registry

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
	noderegistry "github.com/alperencelik/kubemox/pkg/apiserver/registry/node"
	taskregistry "github.com/alperencelik/kubemox/pkg/apiserver/registry/task"
	vmregistry "github.com/alperencelik/kubemox/pkg/apiserver/registry/virtualmachine"
)

// Install registers the ops API group on server.
func Install(server *genericapiserver.GenericAPIServer, resolver *proxmoxconn.Resolver,
	scheme *runtime.Scheme, codecs serializer.CodecFactory) error {

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(
		pveopsv1alpha1.GroupName, scheme, runtime.NewParameterCodec(scheme), codecs,
	)

	apiGroupInfo.VersionedResourcesStorageMap = map[string]map[string]rest.Storage{
		pveopsv1alpha1.SchemeGroupVersion.Version: {
			"virtualmachines":          vmregistry.NewStorage(resolver),
			"virtualmachines/metrics":  vmregistry.NewMetricsStorage(resolver),
			"virtualmachines/start":    vmregistry.NewPowerStorage(resolver, vmregistry.PowerVerbStart),
			"virtualmachines/stop":     vmregistry.NewPowerStorage(resolver, vmregistry.PowerVerbStop),
			"virtualmachines/reboot":   vmregistry.NewPowerStorage(resolver, vmregistry.PowerVerbReboot),
			"virtualmachines/shutdown": vmregistry.NewPowerStorage(resolver, vmregistry.PowerVerbShutdown),
			"nodes":                    noderegistry.NewStorage(resolver),
			"tasks":                    taskregistry.NewStorage(resolver),
		},
	}

	return server.InstallAPIGroup(&apiGroupInfo)
}
