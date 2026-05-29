package server

import (
	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	openapicommon "k8s.io/kube-openapi/pkg/common"
)

// getOpenAPIDefinitions is the indirection used by server.go. It calls
// into the openapi-gen-produced GetOpenAPIDefinitions in the ops package
// (see api/ops/v1alpha1/zz_generated.openapi.go). Keeping the wrapper here
// means the server package owns the contract; the generated file can be
// regenerated freely without touching the bootstrap.
func getOpenAPIDefinitions(ref openapicommon.ReferenceCallback) map[string]openapicommon.OpenAPIDefinition {
	return pveopsv1alpha1.GetOpenAPIDefinitions(ref)
}
