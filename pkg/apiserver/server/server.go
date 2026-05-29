// Package server builds and runs the kubemox aggregated apiserver. It uses
// the standard k8s.io/apiserver "RecommendedOptions" path with etcd
// disabled — every resource served here is a live projection of Proxmox
// state, not stored in etcd.
package server

import (
	"context"
	"fmt"
	"net"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	apiopenapi "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/options"
	clientrest "k8s.io/client-go/rest"
	basecompatibility "k8s.io/component-base/compatibility"
	baseversion "k8s.io/component-base/version"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	pveopsv1alpha1 "github.com/alperencelik/kubemox/api/ops/v1alpha1"
	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/apiserver/proxmoxconn"
	"github.com/alperencelik/kubemox/pkg/apiserver/registry"
)

// Options carries the user-facing flags. Most are inherited from
// options.RecommendedOptions.
type Options struct {
	Recommended *options.RecommendedOptions
}

// NewOptions returns Options preconfigured with apiserver defaults plus
// Etcd disabled.
func NewOptions() *Options {
	o := &Options{
		Recommended: options.NewRecommendedOptions(
			"", // no etcd path prefix
			Codecs.LegacyCodec(pveopsv1alpha1.SchemeGroupVersion),
		),
	}
	o.Recommended.Etcd = nil
	return o
}

// AddFlags registers every RecommendedOptions flag (secure-serving,
// authentication, authorization, audit, ...) on the given FlagSet.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.Recommended.AddFlags(fs)
}

// NewCommand returns a cobra.Command that runs the apiserver. Use this
// from cmd/apiserver/main.go so the binary picks up all RecommendedOptions
// flags (--tls-cert-file, --secure-port, etc.) from the deployment args.
func NewCommand(ctx context.Context) *cobra.Command {
	opts := NewOptions()
	cmd := &cobra.Command{
		Use:   "kubemox-apiserver",
		Short: "Aggregated apiserver for the ops.proxmox.alperen.cloud API group",
		// Silence usage on Run errors — the kube-apiserver logs are the
		// authoritative diagnostic and the cobra usage dump just clutters them.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return opts.Run(cmd.Context())
		},
	}
	cmd.SetContext(ctx)
	opts.AddFlags(cmd.Flags())
	return cmd
}

// Run builds the apiserver, wires storage, and serves until ctx is done.
// Flags must already be parsed before calling.
func (o *Options) Run(ctx context.Context) error {
	if errs := o.Recommended.Validate(); len(errs) > 0 {
		return fmt.Errorf("invalid options: %v", errs)
	}

	// MaybeDefaultWithSelfSignedCerts is a no-op when --tls-cert-file is
	// already set (the production path: cert-manager mounts the cert).
	// For `make run-apiserver` dev it self-signs into --cert-dir.
	if err := o.Recommended.SecureServing.MaybeDefaultWithSelfSignedCerts(
		"kubemox-apiserver.kubemox-system.svc",
		nil,
		[]net.IP{net.ParseIP("127.0.0.1")},
	); err != nil {
		return fmt.Errorf("default serving certs: %w", err)
	}

	serverConfig := genericapiserver.NewRecommendedConfig(Codecs)
	// EffectiveVersion must be non-nil — Config.Complete dereferences it
	// (calls EmulationVersion()). Derived from the k8s.io/apiserver lib we
	// build against, so a dependency bump can't silently lie about the
	// version this server emulates.
	serverConfig.EffectiveVersion = basecompatibility.NewEffectiveVersionFromString(
		baseversion.DefaultKubeBinaryVersion, "", "")
	// OpenAPIV3Config must be non-nil — InstallAPIGroup requires it for
	// SSA type-converter construction. We register a stub GetOpenAPIDefinitions
	// (see openapi.go) until proper schemas are generated.
	namer := apiopenapi.NewDefinitionNamer(Scheme)
	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(getOpenAPIDefinitions, namer)
	serverConfig.OpenAPIConfig.Info.Title = "kubemox-apiserver"
	serverConfig.OpenAPIConfig.Info.Version = "v1alpha1"
	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(getOpenAPIDefinitions, namer)
	serverConfig.OpenAPIV3Config.Info.Title = "kubemox-apiserver"
	serverConfig.OpenAPIV3Config.Info.Version = "v1alpha1"
	if err := o.Recommended.ApplyTo(serverConfig); err != nil {
		return fmt.Errorf("apply options: %w", err)
	}

	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("get rest config: %w", err)
	}

	resolver, err := newResolver(ctx, restCfg)
	if err != nil {
		return fmt.Errorf("build proxmox resolver: %w", err)
	}

	completed := serverConfig.Complete()
	genericServer, err := completed.New("kubemox-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return fmt.Errorf("create generic apiserver: %w", err)
	}

	if err := registry.Install(genericServer, resolver, Scheme, Codecs); err != nil {
		return fmt.Errorf("install ops API group: %w", err)
	}

	return genericServer.PrepareRun().RunWithContext(ctx)
}

// newResolver builds a watch-cached controller-runtime client over the
// kubemox CRs we need (VirtualMachine, ProxmoxConnection).
func newResolver(ctx context.Context, restCfg *clientrest.Config) (*proxmoxconn.Resolver, error) {
	scheme := runtime.NewScheme()
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))

	c, err := cache.New(restCfg, cache.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("build cache: %w", err)
	}

	go func() {
		_ = c.Start(ctx)
	}()
	if !c.WaitForCacheSync(ctx) {
		return nil, fmt.Errorf("cache failed to sync")
	}

	client, err := ctrlclient.New(restCfg, ctrlclient.Options{
		Scheme: scheme,
		Cache:  &ctrlclient.CacheOptions{Reader: c},
	})
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}

	return proxmoxconn.New(client), nil
}
