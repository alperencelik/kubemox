/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/alperencelik/kube-external-watcher/watcher"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	proxmoxcontroller "github.com/alperencelik/kubemox/internal/controller/proxmox"
	"github.com/alperencelik/kubemox/pkg/metrics"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	"github.com/alperencelik/kubemox/pkg/utils"
	// +kubebuilder:scaffold:imports
)

var (
	scheme                = runtime.NewScheme()
	setupLog              = ctrl.Log.WithName("setup")
	metricsUpdateInterval = 30 * time.Second
)

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(certmanagerv1.AddToScheme(scheme))

	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")

	flag.BoolVar(&proxmox.EnableProxmoxTaskLogs, "enable-task-logs", proxmox.EnableProxmoxTaskLogs,
		"Enable verbose task logging specific to Proxmox tasks. Can also be set via ENABLE_PROXMOX_TASK_LOG env var.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "ecb2f1ff.alperen.cloud",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,

		// PprofBindAddress is the TCP address that the controller should bind to
		// for serving pprof. Specify the manager address and the port that should be bind.
		// PprofBindAddress: ":8082",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	PodNamespace := utils.EnsurePodNamespaceEnv()
	setupLog.Info("Pod namespace has been found as:", "POD_NAMESPACE", PodNamespace)

	// Watcher setup

	watcherLogger := ctrl.Log.WithName("external-watcher")
	// Skip status-only updates (generation unchanged) to avoid re-registration noise
	generationFilter := watcher.EventFilter{
		Update: func(oldObj, newObj client.Object) bool {
			return oldObj.GetGeneration() != newObj.GetGeneration()
		},
	}
	watcherOpts := []watcher.Option{
		watcher.WithDefaultPollInterval(60 * time.Second),
		watcher.WithLogger(watcherLogger),
	}
	// Readiness retry config for auto-registered watchers to handle transient API errors during startup
	readinessRetry := watcher.ReadinessRetryConfig{
		InitialInterval: 5 * time.Second,
	}

	// Create external watchers for each resource type with auto-register
	vmWatcher := watcher.NewExternalWatcher(&proxmox.VirtualMachineFetcher{Client: mgr.GetClient()},
		append(watcherOpts,
			watcher.WithMetrics("VirtualMachine"),
			watcher.WithAutoRegister(mgr.GetCache(), &proxmoxv1alpha1.VirtualMachine{},
				proxmox.VMConfigExtractor, readinessRetry),
			watcher.WithAutoRegisterFilter(generationFilter),
		)...)
	managedVMWatcher := watcher.NewExternalWatcher(&proxmox.ManagedVirtualMachineFetcher{Client: mgr.GetClient()},
		append(watcherOpts,
			watcher.WithMetrics("ManagedVirtualMachine"),
			watcher.WithAutoRegister(mgr.GetCache(), &proxmoxv1alpha1.ManagedVirtualMachine{},
				proxmox.ManagedVMConfigExtractor, readinessRetry),
			watcher.WithAutoRegisterFilter(generationFilter),
		)...)
	containerWatcher := watcher.NewExternalWatcher(&proxmox.ContainerFetcher{Client: mgr.GetClient()},
		append(watcherOpts,
			watcher.WithMetrics("Container"),
			watcher.WithAutoRegister(mgr.GetCache(), &proxmoxv1alpha1.Container{},
				proxmox.ContainerConfigExtractor, readinessRetry),
			watcher.WithAutoRegisterFilter(generationFilter),
		)...)
	vmTemplateWatcher := watcher.NewExternalWatcher(&proxmox.VirtualMachineTemplateFetcher{Client: mgr.GetClient()},
		append(watcherOpts,
			watcher.WithMetrics("VirtualMachineTemplate"),
			watcher.WithAutoRegister(mgr.GetCache(), &proxmoxv1alpha1.VirtualMachineTemplate{},
				proxmox.VMTemplateConfigExtractor, readinessRetry),
			watcher.WithAutoRegisterFilter(generationFilter),
		)...)

	// Register watchers as manager runnables
	for _, ew := range []*watcher.ExternalWatcher{vmWatcher, managedVMWatcher, containerWatcher, vmTemplateWatcher} {
		if err = mgr.Add(ew); err != nil {
			setupLog.Error(err, "unable to add external watcher to manager")
			os.Exit(1)
		}
	}

	// Controller setup

	if err = (&proxmoxcontroller.VirtualMachineReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Watcher:  vmWatcher,
		EventCh:  vmWatcher.EventChannel(),
		Recorder: mgr.GetEventRecorder("VirtualMachine"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachine")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.ManagedVirtualMachineReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("ManagedVirtualMachine"),
		EventCh:  managedVMWatcher.EventChannel(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ManagedVirtualMachine")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.VirtualMachineSetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachineSet")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.VirtualMachineSnapshotReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachineSnapshot")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.VirtualMachineSnapshotPolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachineSnapshotPolicy")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.ContainerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("Container"),
		EventCh:  containerWatcher.EventChannel(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Container")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.CustomCertificateReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CustomCertificate")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.StorageDownloadURLReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StorageDownloadURL")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.VirtualMachineTemplateReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		EventCh:  vmTemplateWatcher.EventChannel(),
		Recorder: mgr.GetEventRecorder("VirtualMachineTemplate"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "VirtualMachineTemplate")
		os.Exit(1)
	}
	if err = (&proxmoxcontroller.ProxmoxConnectionReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ProxmoxConnection")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	startMetricsUpdater(context.Background(), mgr.GetClient())

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func startMetricsUpdater(ctx context.Context, kubeClient client.Client) {
	go func() {
		ticker := time.NewTicker(metricsUpdateInterval)
		defer ticker.Stop()
		for range ticker.C {
			// Update metrics here
			metrics.UpdateProxmoxMetrics(ctx, kubeClient)
		}
	}()
}
