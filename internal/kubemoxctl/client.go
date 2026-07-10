package kubemoxctl

import (
	"fmt"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// NewClient builds a dynamic client from a kubeconfig path and optional
// context name. An empty kubeconfig path falls back to the usual loading rules
// (KUBECONFIG env var, then ~/.kube/config).
func NewClient(kubeconfig, contextName string) (dynamic.Interface, *rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("build rest config: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return dyn, cfg, nil
}
