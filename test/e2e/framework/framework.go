package framework

import (
	"fmt"
	"os"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// E2EConfig holds the configuration for e2e tests loaded from YAML.
type E2EConfig struct {
	Proxmox  ProxmoxConfig  `yaml:"proxmox"`
	Operator OperatorConfig `yaml:"operator"`
	Timeouts TimeoutConfig  `yaml:"timeouts"`
}

type ProxmoxConfig struct {
	Endpoint           string `yaml:"endpoint"`
	HostEndpoint       string `yaml:"hostEndpoint"`
	Port               int    `yaml:"port"`
	NodeName           string `yaml:"nodeName"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

type OperatorConfig struct {
	Namespace      string `yaml:"namespace"`
	DeploymentName string `yaml:"deploymentName"`
}

type TimeoutConfig struct {
	ResourceReady    time.Duration `yaml:"resourceReady"`
	ProxmoxOperation time.Duration `yaml:"proxmoxOperation"`
	Cleanup          time.Duration `yaml:"cleanup"`
}

// Framework provides test utilities for e2e tests.
type Framework struct {
	Config     *E2EConfig
	KubeClient client.Client
	RestConfig *rest.Config
	Scheme     *runtime.Scheme
}

// NewFramework creates a new e2e test framework from the given config path.
func NewFramework(configPath string) (*Framework, error) {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading e2e config: %w", err)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding client-go scheme: %w", err)
	}
	if err := proxmoxv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding proxmox scheme: %w", err)
	}

	restConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("getting kube config: %w", err)
	}

	kubeClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating kube client: %w", err)
	}

	return &Framework{
		Config:     cfg,
		KubeClient: kubeClient,
		RestConfig: restConfig,
		Scheme:     scheme,
	}, nil
}

// ProxmoxEndpoint returns the full Proxmox API URL for in-cluster access.
func (f *Framework) ProxmoxEndpoint() string {
	return fmt.Sprintf("https://%s:%d/api2/json", f.Config.Proxmox.Endpoint, f.Config.Proxmox.Port)
}

func loadConfig(path string) (*E2EConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg E2EConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Proxmox.Port == 0 {
		cfg.Proxmox.Port = 8006
	}
	if cfg.Timeouts.ResourceReady == 0 {
		cfg.Timeouts.ResourceReady = 5 * time.Minute
	}
	if cfg.Timeouts.ProxmoxOperation == 0 {
		cfg.Timeouts.ProxmoxOperation = 10 * time.Minute
	}
	if cfg.Timeouts.Cleanup == 0 {
		cfg.Timeouts.Cleanup = 2 * time.Minute
	}
	return &cfg, nil
}
