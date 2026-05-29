package proxmox

import (
	"context"
	"fmt"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"
)

// This file holds the ctx-aware helpers that back the aggregated apiserver
// (pkg/apiserver/...). They intentionally do NOT block on Proxmox task
// completion — the apiserver returns a TaskRef and lets clients poll via
// /apis/ops.proxmox.alperen.cloud/v1alpha1/tasks/{upid}.
//
// The older helpers in virtualmachine.go (StartVM, StopVM, RestartVM, ...)
// still exist; they wait for completion and are kept for the operator's
// reconcile loops.

// TaskStatus is the projection returned by GetTaskStatus.
type TaskStatus struct {
	UPID       string
	Node       string
	Type       string
	User       string
	Status     string // "running" or "stopped"
	ExitStatus string // populated when finished — "OK" or an error string
	StartTime  time.Time
	EndTime    time.Time // zero if still running
}

// GetTaskStatus pings Proxmox for the current status of a task by UPID.
// The node is encoded in the UPID itself (segment 1 of the colon-split).
func (pc *ProxmoxClient) GetTaskStatus(ctx context.Context, upid string) (*TaskStatus, error) {
	if upid == "" {
		return nil, fmt.Errorf("upid is required")
	}
	t := proxmox.NewTask(proxmox.UPID(upid), pc.Client)
	if t == nil || t.Node == "" {
		return nil, fmt.Errorf("invalid UPID %q", upid)
	}
	if err := t.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping task %q: %w", upid, err)
	}
	return &TaskStatus{
		UPID:       upid,
		Node:       t.Node,
		Type:       t.Type,
		User:       t.User,
		Status:     t.Status,
		ExitStatus: t.ExitStatus,
		StartTime:  t.StartTime,
		EndTime:    t.EndTime,
	}, nil
}

// VMMetrics is the rich projection returned by GetVMMetrics.
// Values are point-in-time samples from /nodes/{node}/qemu/{vmid}/status/current
// as exposed by go-proxmox's VirtualMachine.Ping.
type VMMetrics struct {
	State            string
	CPUUsage         float64 // fraction in [0,1] across vCPUs
	CPUCount         int
	MemoryUsedBytes  int64
	MemoryTotalBytes int64
	DiskReadBytes    int64
	DiskWriteBytes   int64
	NetInBytes       int64
	NetOutBytes      int64
	UptimeSeconds    int64
	VMID             int
}

// GetVMMetrics returns a point-in-time sample of CPU/memory/disk/net.
// Cumulative counters (DiskRead/Write, NetIn/Out) are since the VM's
// last boot; Prometheus consumers should compute rates.
func (pc *ProxmoxClient) GetVMMetrics(ctx context.Context, vmName, nodeName string) (*VMMetrics, error) {
	vm, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		return nil, err
	}
	if err := vm.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping VM %q: %w", vmName, err)
	}
	return &VMMetrics{
		State:            vm.Status,
		CPUUsage:         vm.CPU,
		CPUCount:         vm.CPUs,
		MemoryUsedBytes:  int64(vm.Mem),
		MemoryTotalBytes: int64(vm.MaxMem),
		DiskReadBytes:    int64(vm.DiskRead),
		DiskWriteBytes:   int64(vm.DiskWrite),
		NetInBytes:       int64(vm.NetIn),
		NetOutBytes:      int64(vm.Netout),
		UptimeSeconds:    int64(vm.Uptime),
		VMID:             int(vm.VMID),
	}, nil
}

// PowerVerb names the async power op to perform. Mirrors the registry
// PowerVerb enum but exists here so pkg/proxmox stays self-contained.
type PowerVerb string

const (
	PowerStart    PowerVerb = "start"
	PowerStop     PowerVerb = "stop"
	PowerReboot   PowerVerb = "reboot"
	PowerShutdown PowerVerb = "shutdown"
)

// PowerVMAsync triggers the named power op and returns the Proxmox UPID
// without waiting for completion. Unlike StartVM/StopVM/RestartVM in
// virtualmachine.go, this does not call WaitForCompleteStatus — the
// apiserver is responsible for surfacing the task ref to the client.
func (pc *ProxmoxClient) PowerVMAsync(ctx context.Context, vmName, nodeName string,
	verb PowerVerb) (string, error) {
	vm, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		return "", err
	}
	var task *proxmox.Task
	switch verb {
	case PowerStart:
		task, err = vm.Start(ctx)
	case PowerStop:
		task, err = vm.Stop(ctx)
	case PowerReboot:
		task, err = vm.Reboot(ctx)
	case PowerShutdown:
		task, err = vm.Shutdown(ctx)
	default:
		return "", fmt.Errorf("unknown power verb %q", verb)
	}
	if err != nil {
		return "", fmt.Errorf("%s VM %q: %w", verb, vmName, err)
	}
	if task == nil {
		// Some versions of go-proxmox return (nil, nil) when the VM was
		// already in the desired state. Treat as success with no task.
		return "", nil
	}
	return string(task.UPID), nil
}
