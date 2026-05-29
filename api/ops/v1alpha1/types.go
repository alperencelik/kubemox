package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VirtualMachine is a live projection of a Proxmox VM, scoped by the kubemox
// VirtualMachine CR of the same name. The Spec mirrors the desired kubemox
// state (read-only here); LiveStatus reflects what Proxmox reports right now.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSpec   `json:"spec,omitempty"`
	Status VirtualMachineStatus `json:"status,omitempty"`
}

// VirtualMachineSpec is a read-only snapshot of the kubemox CR spec keys
// most relevant to ops. The aggregated apiserver does not accept writes here;
// modify the underlying kubemox VirtualMachine CR instead.
type VirtualMachineSpec struct {
	// NodeName is the Proxmox node hosting this VM.
	NodeName string `json:"nodeName,omitempty"`
	// ConnectionName is the ProxmoxConnection CR backing the VM.
	ConnectionName string `json:"connectionName,omitempty"`
	// VMID is the Proxmox numeric ID (resolved live).
	VMID int `json:"vmID,omitempty"`
}

// VirtualMachineStatus is the live Proxmox state.
type VirtualMachineStatus struct {
	// State is "running", "stopped", or "unknown".
	State string `json:"state,omitempty"`
	// UptimeSeconds since last boot, 0 if stopped.
	UptimeSeconds int64 `json:"uptimeSeconds,omitempty"`
	// IPAddress reported by qemu-guest-agent, if available.
	IPAddress string `json:"ipAddress,omitempty"`
	// OSInfo from qemu-guest-agent or ostype config.
	OSInfo string `json:"osInfo,omitempty"`
}

// VirtualMachineList is a list of VM projections.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type VirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachine `json:"items"`
}

// VirtualMachineMetrics is the response of the /metrics subresource. Values
// are point-in-time samples from Proxmox RRD; rates and historical series
// are not provided here — point Prometheus at the pve_exporter for those.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type VirtualMachineMetrics struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Timestamp is when the sample was taken.
	Timestamp metav1.Time `json:"timestamp"`

	// CPUUsage is a fraction in [0,1] across all vCPUs.
	CPUUsage float64 `json:"cpuUsage"`
	// CPUCount is the number of vCPUs allocated.
	CPUCount int `json:"cpuCount"`

	// MemoryUsedBytes currently allocated.
	MemoryUsedBytes int64 `json:"memoryUsedBytes"`
	// MemoryTotalBytes available to the guest.
	MemoryTotalBytes int64 `json:"memoryTotalBytes"`

	// DiskReadBytes / DiskWriteBytes — cumulative since boot.
	DiskReadBytes  int64 `json:"diskReadBytes"`
	DiskWriteBytes int64 `json:"diskWriteBytes"`

	// NetInBytes / NetOutBytes — cumulative since boot.
	NetInBytes  int64 `json:"netInBytes"`
	NetOutBytes int64 `json:"netOutBytes"`

	// UptimeSeconds since last boot.
	UptimeSeconds int64 `json:"uptimeSeconds"`
}

// PowerAction is the request/response body for power-lifecycle verbs
// (/start, /stop, /reboot, /shutdown). On request, fields are optional
// (callers may POST an empty body). On response, TaskRef points at the
// Proxmox task; clients poll /apis/ops.proxmox.alperen.cloud/v1alpha1/tasks/{upid}.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PowerAction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// ForceStop, when true on /stop, kills the VM instead of an ACPI shutdown.
	ForceStop bool `json:"forceStop,omitempty"`
	// TimeoutSeconds bounds how long Proxmox waits for ACPI shutdown.
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// TaskRef is set on the response only.
	TaskRef *TaskRef `json:"taskRef,omitempty"`
}

// TaskRef is the minimal identity needed to poll a Proxmox task.
type TaskRef struct {
	// UPID is the Proxmox unique task identifier (the full UPID string).
	UPID string `json:"upid"`
	// Node is where the task is running.
	Node string `json:"node"`
}

// Task is a live projection of a Proxmox task. Returned by GET on the
// /tasks resource.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Task struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Node the task is running on.
	Node string `json:"node"`
	// Type is the Proxmox task type (e.g. "qmstart", "qmstop").
	Type string `json:"type"`
	// User that initiated the task in Proxmox.
	User string `json:"user,omitempty"`
	// Status: "running", "stopped".
	Status string `json:"status"`
	// ExitStatus: present when finished — "OK" or an error string.
	ExitStatus string `json:"exitStatus,omitempty"`
	// StartTime when Proxmox started the task.
	StartTime metav1.Time `json:"startTime,omitempty"`
	// EndTime when the task finished, if it has.
	EndTime *metav1.Time `json:"endTime,omitempty"`
}

// TaskList is a list of Tasks across all nodes.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Task `json:"items"`
}

// Node is a live projection of a Proxmox cluster node.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type Node struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// ConnectionName is the ProxmoxConnection CR this node was discovered through.
	ConnectionName string `json:"connectionName,omitempty"`
	// Status: "online" or "offline".
	Status string `json:"status"`
	// CPUUsage is a fraction in [0,1].
	CPUUsage float64 `json:"cpuUsage"`
	// CPUCount allocated on this node.
	CPUCount int `json:"cpuCount"`
	// MemoryUsedBytes / MemoryTotalBytes.
	MemoryUsedBytes  int64 `json:"memoryUsedBytes"`
	MemoryTotalBytes int64 `json:"memoryTotalBytes"`
	// UptimeSeconds since last boot.
	UptimeSeconds int64 `json:"uptimeSeconds"`
	// Version of pve-manager running.
	Version string `json:"version,omitempty"`
}

// NodeList is a list of Nodes across all known ProxmoxConnections.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Node `json:"items"`
}
