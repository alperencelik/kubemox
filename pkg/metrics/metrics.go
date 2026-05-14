package metrics

import (
	"context"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var nameNamespaceLabels = []string{"name", "namespace"}

var (
	// VirtualMachine
	virtualMachineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_count",
		Help: "Number of virtualMachines registered in the cluster",
	})
	virtualMachineCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_cpu_cores",
		Help: "Number of CPU cores of virtualMachine",
	}, nameNamespaceLabels)
	virtualMachineMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_memory",
		Help: "Memory of virtualMachine as MB",
	}, nameNamespaceLabels)
	virtualMachineRunningCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_running_count",
		Help: "Number of running virtualMachines",
	})
	virtualMachineStoppedCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_stopped_count",
		Help: "Number of stopped virtualMachines",
	})

	// Container
	containerCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_container_count",
		Help: "Number of containers registered in the cluster",
	})
	containerCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_container_cpu_cores",
		Help: "Number of CPU cores of container",
	}, nameNamespaceLabels)
	containerMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_container_memory",
		Help: "Memory of container as MB",
	}, nameNamespaceLabels)

	// VirtualMachineTemplate
	virtualMachineTemplateCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_template_count",
		Help: "Number of virtualMachineTemplates registered in the cluster",
	})
	virtualMachineTemplateCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_template_cpu_cores",
		Help: "Number of CPU cores of virtualMachineTemplate",
	}, nameNamespaceLabels)
	virtualMachineTemplateMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_template_memory",
		Help: "Memory of virtualMachineTemplate as MB",
	}, nameNamespaceLabels)

	// VirtualMachineSet
	virtualMachineSetCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_set_count",
		Help: "Number of virtualMachineSets registered in the cluster",
	})
	virtualMachineSetCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_set_cpu_cores",
		Help: "Number of CPU cores of virtualMachineSet",
	}, nameNamespaceLabels)
	virtualMachineSetMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_set_memory",
		Help: "Memory of virtualMachineSet as MB",
	}, nameNamespaceLabels)
	virtualMachineSetReplicas = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_set_replicas",
		Help: "Number of replicas of virtualMachineSet",
	}, nameNamespaceLabels)

	// VirtualMachineSnapshot
	virtualMachineSnapshotCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_snapshot_count",
		Help: "Number of virtualMachineSnapshots registered in the cluster",
	})
	virtualMachineSnapshotPerVirtualMachineCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_snapshots",
		Help: "Number of snapshots of virtualMachine",
	}, nameNamespaceLabels)

	// VirtualMachineSnapshotPolicy
	virtualMachineSnapshotPolicyCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_snapshot_policy_count",
		Help: "Number of virtualMachineSnapshotPolicies registered in the cluster",
	})

	// StorageDownloadURL
	storageDownloadURLCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_storage_download_url_count",
		Help: "Number of storageDownloadURLs registered in the cluster",
	})

	// CustomCertificate
	customCertificateCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_custom_certificate_count",
		Help: "Number of customCertificates registered in the cluster",
	})
)

var KubemoxMetrics = []prometheus.Collector{
	// VirtualMachine
	virtualMachineCount,
	virtualMachineCPUCores,
	virtualMachineMemory,
	virtualMachineRunningCount,
	virtualMachineStoppedCount,
	// Container
	containerCount,
	containerCPUCores,
	containerMemory,
	// VirtualMachineTemplate
	virtualMachineTemplateCount,
	virtualMachineTemplateCPUCores,
	virtualMachineTemplateMemory,
	// VirtualMachineSet
	virtualMachineSetCount,
	virtualMachineSetCPUCores,
	virtualMachineSetMemory,
	virtualMachineSetReplicas,
	// VirtualMachineSnapshot
	virtualMachineSnapshotCount,
	virtualMachineSnapshotPerVirtualMachineCount,
	// VirtualMachineSnapshotPolicy
	virtualMachineSnapshotPolicyCount,
	// StorageDownloadURL
	storageDownloadURLCount,
	// CustomCertificate
	customCertificateCount,
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	metrics.Registry.MustRegister(KubemoxMetrics...)
}

func UpdateProxmoxMetrics(ctx context.Context, kubeClient client.Client) {
	logger := log.FromContext(ctx)

	// Get all virtual machines
	vmList := &proxmoxv1alpha1.VirtualMachineList{}
	if err := kubeClient.List(ctx, vmList); err != nil {
		logger.Error(err, "unable to list virtual machines")
		return
	}
	updateVirtualMachineMetrics(vmList)

	// Get all containers
	containerList := &proxmoxv1alpha1.ContainerList{}
	if err := kubeClient.List(ctx, containerList); err != nil {
		logger.Error(err, "unable to list containers")
		return
	}
	updateContainerMetrics(containerList)

	// Get all virtual machine templates
	virtualMachineTemplateList := &proxmoxv1alpha1.VirtualMachineTemplateList{}
	if err := kubeClient.List(ctx, virtualMachineTemplateList); err != nil {
		logger.Error(err, "unable to list virtual machine templates")
		return
	}
	updateVirtualMachineTemplateMetrics(virtualMachineTemplateList)

	// Get all virtual machine sets
	vmSetList := &proxmoxv1alpha1.VirtualMachineSetList{}
	if err := kubeClient.List(ctx, vmSetList); err != nil {
		logger.Error(err, "unable to list virtual machine sets")
		return
	}
	updateVirtualMachineSetMetrics(vmSetList)

	// Get all virtual machine snapshots
	vmSnapshotList := &proxmoxv1alpha1.VirtualMachineSnapshotList{}
	if err := kubeClient.List(ctx, vmSnapshotList); err != nil {
		logger.Error(err, "unable to list virtual machine snapshots")
		return
	}
	updateVirtualMachineSnapshotMetrics(vmSnapshotList)

	// Get all virtual machine snapshot policies
	vmSnapshotPolicyList := &proxmoxv1alpha1.VirtualMachineSnapshotPolicyList{}
	if err := kubeClient.List(ctx, vmSnapshotPolicyList); err != nil {
		logger.Error(err, "unable to list virtual machine snapshot policies")
		return
	}
	virtualMachineSnapshotPolicyCount.Set(float64(len(vmSnapshotPolicyList.Items)))

	// Get all storage download URLs
	storageDownloadURLList := &proxmoxv1alpha1.StorageDownloadURLList{}
	if err := kubeClient.List(ctx, storageDownloadURLList); err != nil {
		logger.Error(err, "unable to list storage download URLs")
		return
	}
	storageDownloadURLCount.Set(float64(len(storageDownloadURLList.Items)))

	// Get all custom certificates
	customCertificateList := &proxmoxv1alpha1.CustomCertificateList{}
	if err := kubeClient.List(ctx, customCertificateList); err != nil {
		logger.Error(err, "unable to list custom certificates")
		return
	}
	customCertificateCount.Set(float64(len(customCertificateList.Items)))
}

func updateVirtualMachineMetrics(vmList *proxmoxv1alpha1.VirtualMachineList) {
	virtualMachineCount.Set(float64(len(vmList.Items)))
	for i := range vmList.Items {
		vm := &vmList.Items[i]
		virtualMachineCPUCores.WithLabelValues(vm.Name, vm.Namespace).Set(float64(proxmox.GetCores(vm)))
		virtualMachineMemory.WithLabelValues(vm.Name, vm.Namespace).Set(float64(proxmox.GetMemory(vm)))
	}
}

func updateContainerMetrics(containerList *proxmoxv1alpha1.ContainerList) {
	containerCount.Set(float64(len(containerList.Items)))
	for i := range containerList.Items {
		container := &containerList.Items[i]
		containerCPUCores.WithLabelValues(container.Name, container.Namespace).Set(float64(container.Spec.Template.Cores))
		containerMemory.WithLabelValues(container.Name, container.Namespace).Set(float64(container.Spec.Template.Memory))
	}
}

func updateVirtualMachineTemplateMetrics(virtualMachineTemplateList *proxmoxv1alpha1.VirtualMachineTemplateList) {
	virtualMachineTemplateCount.Set(float64(len(virtualMachineTemplateList.Items)))
	for i := range virtualMachineTemplateList.Items {
		vmTemplate := &virtualMachineTemplateList.Items[i]
		virtualMachineTemplateCPUCores.WithLabelValues(vmTemplate.Name, vmTemplate.Namespace).
			Set(float64(vmTemplate.Spec.VirtualMachineConfig.Cores))
		virtualMachineTemplateMemory.WithLabelValues(vmTemplate.Name, vmTemplate.Namespace).
			Set(float64(vmTemplate.Spec.VirtualMachineConfig.Memory))
	}
}

func updateVirtualMachineSetMetrics(vmSetList *proxmoxv1alpha1.VirtualMachineSetList) {
	virtualMachineSetCount.Set(float64(len(vmSetList.Items)))
	for i := range vmSetList.Items {
		vmSet := &vmSetList.Items[i]
		virtualMachineSetCPUCores.WithLabelValues(vmSet.Name, vmSet.Namespace).
			Set(float64(vmSet.Spec.Template.Cores))
		virtualMachineSetMemory.WithLabelValues(vmSet.Name, vmSet.Namespace).
			Set(float64(vmSet.Spec.Template.Memory))
		virtualMachineSetReplicas.WithLabelValues(vmSet.Name, vmSet.Namespace).
			Set(float64(vmSet.Spec.Replicas))
	}
}

func updateVirtualMachineSnapshotMetrics(vmSnapshotList *proxmoxv1alpha1.VirtualMachineSnapshotList) {
	virtualMachineSnapshotCount.Set(float64(len(vmSnapshotList.Items)))
}
