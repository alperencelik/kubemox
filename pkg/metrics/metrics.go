package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	virtualMachineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_count",
		Help: "Number of virtualMachines registered in the cluster",
	})
	virtualMachineCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_cpu_cores",
		Help: "Number of CPU cores of virtualMachine",
	}, []string{"name", "namespace"})
	virtualMachineMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_virtual_machine_memory",
		Help: "Memory of virtualMachine as MB",
	}, []string{"name", "namespace"})
	ManagedVirtualMachineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubemox_managed_virtual_machine_count",
		Help: "Number of managedVirtualMachines exists in Proxmox",
	})
	ManagedVirtualMachineCPUCores = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_managed_virtual_machine_cpu_cores",
		Help: "Number of CPU cores of managedVirtualMachine",
	}, []string{"name", "namespace"})
	ManagedVirtualMachineMemory = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubemox_managed_virtual_machine_memory",
		Help: "Memory of managedVirtualMachine as MB",
	}, []string{"name", "namespace"})
)

func init() {
	metrics.Registry.MustRegister(virtualMachineCount)
	metrics.Registry.MustRegister(virtualMachineCPUCores)
	metrics.Registry.MustRegister(virtualMachineMemory)
	metrics.Registry.MustRegister(ManagedVirtualMachineCount)
	metrics.Registry.MustRegister(ManagedVirtualMachineCPUCores)
	metrics.Registry.MustRegister(ManagedVirtualMachineMemory)
}

func IncVirtualMachineCount() {
	virtualMachineCount.Inc()
}

func DecVirtualMachineCount() {
	virtualMachineCount.Dec()
}

func SetVirtualMachineCPUCores(name, namespace string, cores float64) {
	virtualMachineCPUCores.WithLabelValues(name, namespace).Set(cores)
}

func SetVirtualMachineMemory(name, namespace string, memory float64) {
	virtualMachineMemory.WithLabelValues(name, namespace).Set(memory)
}

func IncManagedVirtualMachineCount() {
	ManagedVirtualMachineCount.Inc()
}

func DecManagedVirtualMachineCount() {
	ManagedVirtualMachineCount.Dec()
}

func SetManagedVirtualMachineCPUCores(name, namespace string, cores float64) {
	ManagedVirtualMachineCPUCores.WithLabelValues(name, namespace).Set(cores)
}

func SetManagedVirtualMachineMemory(name, namespace string, memory float64) {
	ManagedVirtualMachineMemory.WithLabelValues(name, namespace).Set(memory)
}
