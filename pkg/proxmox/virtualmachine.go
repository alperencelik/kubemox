package proxmox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/utils"
	"github.com/google/go-cmp/cmp"
	proxmox "github.com/luthermonson/go-proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type VirtualMachineComparison struct {
	Sockets  int `json:"sockets"`
	Cores    int `json:"cores"`
	Memory   int `json:"memory"`
	Networks []proxmoxv1alpha1.VirtualMachineNetwork
	Disks    []proxmoxv1alpha1.VirtualMachineDisk
}

var mutex = &sync.Mutex{}

const (
	// The tag that will be added to VMs in Proxmox cluster
	VirtualMachineRunningState = "running"
	VirtualMachineStoppedState = "stopped"
	VirtualMachineTemplateType = "template"
	VirtualMachineScratchType  = "scratch"
	virtualMachineCPUOption    = "cores"
	virtualMachineSocketOption = "sockets"
	virtualMachineMemoryOption = "memory"
	// The timeout for qemu-agent to start in seconds
	AgentTimeoutSeconds = 10
	// The timeouts for VirtualMachine operations
	// Timeout = operationTimesNum * operationSteps
	virtualMachineCreateTimesNum  = 15
	virtualMachineCreateSteps     = 10
	virtualMachineStartTimesNum   = 10
	virtualMachineStartSteps      = 3
	virtualMachineStopTimesNum    = 10
	virtualMachineStopSteps       = 3
	virtualMachineRestartTimesNum = 10
	virtualMachineRestartSteps    = 3
	virtualMachineUpdateTimesNum  = 10
	virtualMachineUpdateSteps     = 3
	virtualMachineDeleteTimesNum  = 10
	virtualMachineDeleteSteps     = 3
	// The network name
	netStr = "net"
)

var (
	virtualMachineTag         string
	ManagedVirtualMachineTag  string
	virtualMachineTemplateTag string
)

func init() {
	virtualMachineTag = os.Getenv("VIRTUAL_MACHINE_TAG")
	if virtualMachineTag == "" {
		virtualMachineTag = "kubemox"
	}
	ManagedVirtualMachineTag = os.Getenv("MANAGED_VIRTUAL_MACHINE_TAG")
	if ManagedVirtualMachineTag == "" {
		ManagedVirtualMachineTag = "kubemox-managed-vm"
	}
	virtualMachineTemplateTag = os.Getenv("VIRTUAL_MACHINE_TEMPLATE_TAG")
	if virtualMachineTemplateTag == "" {
		virtualMachineTemplateTag = "kubemox-template"
	}
}

func (pc *ProxmoxClient) CreateVMFromTemplate(vm *proxmoxv1alpha1.VirtualMachine) error {
	nodeName := vm.Spec.NodeName
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return err
	}
	templateVMName := vm.Spec.Template.Name
	templateVM, err := pc.getVirtualMachine(templateVMName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting template VM")
		// If template VM doesn't exist, return the error as unrecoverable
		// Add prefix to the error message
		modifiedErr := fmt.Sprintf("Template VirtualMachine %s not found: %s", templateVMName, err.Error())
		return &NotFoundError{Message: modifiedErr}
	}
	var CloneOptions proxmox.VirtualMachineCloneOptions
	CloneOptions.Full = 1
	CloneOptions.Name = vm.Name
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Creating VM from template: %s", templateVMName))
	// Make sure that not two VMs are created at the exact time
	mutex.Lock()
	newID, task, err := templateVM.Clone(ctx, &CloneOptions)
	if err != nil {
		log.Log.Error(err, "Error creating VM")
		return err
	}
	log.Log.Info(fmt.Sprintf("New VM %s has been creating with ID: %d", vm.Name, newID))
	mutex.Unlock()
	// TODO: Implement a better way to watch the tasks.
	logChan, err := task.Watch(ctx, 0)
	if err != nil {
		return err
	}
	for logEntry := range logChan {
		log.Log.Info(fmt.Sprintf("Virtual Machine %s, creation process: %s", vm.Name, logEntry))
	}
	mutex.Lock()
	// TODO: Switch to task Status
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
		virtualMachineCreateTimesNum, virtualMachineCreateSteps)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't stop VM")
		return taskErr
	}
	// Add tag to VM
	VirtualMachine, err := node.VirtualMachine(ctx, newID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		return err
	}
	task, err = VirtualMachine.AddTag(ctx, virtualMachineTag)
	// TODO: Use strings instead of numbers
	taskStatus, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx, 3, 5)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Error adding tag to VM")
		return taskErr
	}
	mutex.Unlock()
	if err != nil {
		return err
	}
	// Cache the new VM ID
	pc.setCachedVMID(nodeName, vm.Name, newID)
	return nil
}

func (pc *ProxmoxClient) getVMID(vmName, nodeName string) (int, error) {
	// Get node
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return 0, err
	}
	// If it's in the cache, return it directly
	pc.vmIDMutex.RLock()
	if vmID, exists := pc.nodesCache[nodeName].vms[vmName]; exists {
		pc.vmIDMutex.RUnlock()
		return vmID, nil
	}
	// Not in cache, fetch from API
	vmList, err := node.VirtualMachines(ctx)
	if err != nil {
		return 0, err
	}
	for _, vm := range vmList {
		if strings.EqualFold(vm.Name, vmName) {
			vmID := int(vm.VMID)
			// Store in cache
			pc.setCachedVMID(nodeName, vmName, vmID)
			return vmID, nil
		}
	}
	return 0, nil
}

func (pc *ProxmoxClient) CheckVM(vmName, nodeName string) (bool, error) {
	// Check if VM exists
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return false, err
	}
	vmList, err := node.VirtualMachines(ctx)
	if err != nil {
		return false, err
	}
	for _, vm := range vmList {
		// if vm.Name == vmName {
		if strings.EqualFold(vm.Name, vmName) {
			// Cache the VM ID while we're at it
			pc.setCachedVMID(nodeName, vm.Name, int(vm.VMID))
			return true, nil
		}
	}
	return false, nil
}

func (pc *ProxmoxClient) GetVMIPAddress(vmName, nodeName string) string {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		return ""
	}
	// Get VM IP
	VirtualMachineIfaces, err := VirtualMachine.AgentGetNetworkIFaces(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting VM IP")
		return ""
	}
	for _, iface := range VirtualMachineIfaces {
		for _, ip := range iface.IPAddresses {
			return ip.IPAddress
		}
	}
	return ""
}

func (pc *ProxmoxClient) GetOSInfo(vmName, nodeName string) string {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		return ""
	}
	// Get VM OS
	VirtualMachineOS, err := VirtualMachine.AgentOsInfo(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting VM OS")
		return ""
	}
	// Check either the OS name or pretty name is empty
	switch {
	case VirtualMachineOS.Name == "" && VirtualMachineOS.PrettyName == "":
		return VirtualMachineOS.KernelRelease
	case VirtualMachineOS.PrettyName == "":
		return VirtualMachineOS.Name
	default:
		return VirtualMachineOS.PrettyName
	}
}

func (pc *ProxmoxClient) GetVMUptime(vmName, nodeName string) string {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Get VM Uptime as seconds
	VirtualMachineUptime := int(VirtualMachine.Uptime)
	// Convert seconds to format like 1d 2h 3m 4s
	uptime := utils.FormatUptime(VirtualMachineUptime)
	return uptime
}

func (pc *ProxmoxClient) DeleteVM(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		// Make the error as not found error to complete the deletion
		return &NotFoundError{Message: err.Error()}
	}
	// Stop VM
	vmStatus := VirtualMachine.Status
	if vmStatus == VirtualMachineRunningState {
		stopTask, stopErr := VirtualMachine.Stop(ctx)
		if stopErr != nil {
			log.Log.Error(stopErr, "Can't stop VM")
			return &TaskError{ExitStatus: stopTask.ExitStatus}
		}
		taskStatus, taskCompleted, taskErr := stopTask.WaitForCompleteStatus(ctx,
			virtualMachineStopTimesNum, virtualMachineStopSteps)
		if !taskStatus {
			// Return the task.ExitStatus as error
			return &TaskError{ExitStatus: stopTask.ExitStatus}
		}
		if !taskCompleted {
			log.Log.Error(taskErr, "Can't stop VM")
			return taskErr
		}
	}
	// Delete VM
	task, err := VirtualMachine.Delete(ctx)
	if err != nil {
		return err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
		virtualMachineDeleteTimesNum, virtualMachineDeleteSteps)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't delete VM")
		return taskErr
	}
	// Invalidate cache entry for this VM
	pc.vmIDMutex.Lock()
	delete(pc.nodesCache[nodeName].vms, vmName)
	pc.vmIDMutex.Unlock()
	return nil
}

func (pc *ProxmoxClient) StartVM(vmName, nodeName string) (bool, error) {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		return false, err
	}
	// Start VM
	task, err := VirtualMachine.Start(ctx)
	if err != nil {
		return false, err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
		virtualMachineStartTimesNum, virtualMachineStartSteps)
	if !taskStatus {
		// Return the taks.ExitStatus as error
		return false, &TaskError{ExitStatus: task.ExitStatus}
	}
	if taskCompleted {
		return true, taskErr
	} else {
		return false, taskErr
	}
}

func (pc *ProxmoxClient) RestartVM(vmName, nodeName string) (*proxmox.Task, error) {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM to restart")
		return nil, err
	}
	// Restart VM
	task, err := VirtualMachine.Reboot(ctx)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (pc *ProxmoxClient) StopVM(vmName, nodeName string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM to stop")
		return err
	}
	// Stop VM
	task, err := VirtualMachine.Stop(ctx)
	if err != nil {
		return err
	}

	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
		virtualMachineStopTimesNum, virtualMachineStopSteps)
	if !taskStatus {
		// Return the taks.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't stop VM")
		return taskErr
	} else {
		return taskErr
	}
}

func (pc *ProxmoxClient) GetVMState(vmName, nodeName string) (state string, err error) {
	// Gets the VMstate from Proxmox API
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	VirtualMachineState := VirtualMachine.Status
	if err != nil {
		return "unknown", err
	}
	switch VirtualMachineState {
	case VirtualMachineRunningState:
		return VirtualMachineRunningState, nil
	case VirtualMachineStoppedState:
		return VirtualMachineStoppedState, nil
	default:
		return "unknown", err
	}
}

func (pc *ProxmoxClient) AgentIsRunning(vmName, nodeName string) (bool, error) {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for agent check")
		return false, err
	}
	err = VirtualMachine.WaitForAgent(ctx, AgentTimeoutSeconds)
	if err != nil {
		if errors.Is(err, proxmox.ErrTimeout) {
			return false, nil
		}
		return false, err
	} else {
		return true, nil
	}
}

func (pc *ProxmoxClient) CreateVMFromScratch(vm *proxmoxv1alpha1.VirtualMachine) error {
	nodeName := vm.Spec.NodeName
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return err
	}
	virtualMachineSpec := vm.Spec.VMSpec

	osName := vm.Spec.VMSpec.OSImage.Name
	osValue := vm.Spec.VMSpec.OSImage.Value

	// Create VM from scratch
	VMOptions := []proxmox.VirtualMachineOption{
		{
			Name:  virtualMachineSocketOption,
			Value: virtualMachineSpec.Socket,
		},
		{
			Name:  virtualMachineCPUOption,
			Value: virtualMachineSpec.Cores,
		},
		{
			Name:  virtualMachineMemoryOption,
			Value: virtualMachineSpec.Memory,
		},
		{
			Name:  osName,
			Value: osValue + ",media=cdrom",
		},
		{
			Name:  "name",
			Value: vm.Spec.Name,
		},
	}
	// handle the disks and networks
	// for each disk and network, add the disk and network to the VMOptions
	if len(virtualMachineSpec.Disk) != 0 {
		for _, disk := range virtualMachineSpec.Disk {
			VMOptions = append(VMOptions, proxmox.VirtualMachineOption{
				Name:  disk.Device,
				Value: disk.Storage + ":" + strconv.Itoa(disk.Size),
			})
		}
	}
	if virtualMachineSpec.Network != nil {
		for i, network := range *virtualMachineSpec.Network {
			VMOptions = append(VMOptions, proxmox.VirtualMachineOption{
				Name:  "net" + strconv.Itoa(i),
				Value: network.Model + ",bridge=" + network.Bridge,
			})
		}
	}
	// Make sure that not two VMs are created at the exact time
	mutex.Lock()
	// Get next VMID
	vmID, err := getNextVMID(pc.Client)
	if err != nil {
		log.Log.Error(err, "Error getting next VMID")
		return err
	}
	// Create VM
	task, err := node.NewVirtualMachine(ctx, vmID, VMOptions...)
	if err != nil {
		log.Log.Error(err, "Error creating VM")
		return err
	}
	mutex.Unlock()
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 10, 10)
	if !taskStatus {
		// Return the task.ExitStatus as an error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskErr
	}
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return err
	}
	addTagTask, err := VirtualMachine.AddTag(ctx, virtualMachineTag)
	if err != nil {
		return err
	}
	taskStatus, taskCompleted, taskErr = addTagTask.WaitForCompleteStatus(ctx, 3, 10)
	if !taskStatus {
		// Return the task.ExitStatus as an error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskErr
	}
	// Cache the new VM ID
	pc.setCachedVMID(nodeName, vm.Spec.Name, vmID)
	return nil
}

func CheckVMType(vm *proxmoxv1alpha1.VirtualMachine) string {
	var VMType string
	switch {
	case !reflect.ValueOf(vm.Spec.Template).IsZero():
		VMType = VirtualMachineTemplateType
	case !reflect.ValueOf(vm.Spec.VMSpec).IsZero():
		VMType = VirtualMachineScratchType
	case !reflect.ValueOf(vm.Spec.Template).IsZero() && !reflect.ValueOf(vm.Spec.VMSpec).IsZero():
		VMType = "faulty"
	default:
		VMType = "undefined"
	}
	return VMType
}

func CheckManagedVMExists(managedVM string) (bool, error) {
	// Theoretically this should be handled with the reconciler.List method
	// but since this one is used before the reconciler build it's cache
	// we have to retrieve the objects from API server directly
	existingManagedVMNames := []string{}
	// Get managed VMs
	crd, err := kubernetes.GetManagedVMCRD()
	if err != nil {
		return false, err
	}
	customResource := schema.GroupVersionResource{
		Group:    crd.Spec.Group,
		Version:  crd.Spec.Versions[0].Name,
		Resource: crd.Spec.Names.Plural,
	}
	// Get managedVirtualMachine CRD
	ClientManagedVMs, err := kubernetes.DynamicClient.Resource(customResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	// Get all managed VM names as array
	for _, ClientManagedVM := range ClientManagedVMs.Items {
		existingManagedVMNames = append(existingManagedVMNames, ClientManagedVM.GetName())
	}
	// Check if managed VM exists
	return utils.StringInSlice(managedVM, existingManagedVMNames), nil
}

func (pc *ProxmoxClient) GetManagedVMSpec(managedVMName, nodeName string) (cores, memory, disk int) {
	// Get spec of VM
	VirtualMachine, err := pc.getVirtualMachine(managedVMName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for managed VM spec")
	}
	cores = VirtualMachine.CPUs
	memory = int(VirtualMachine.MaxMem / 1024 / 1024) // As MB
	disk = int(VirtualMachine.MaxDisk / 1024 / 1024 / 1024)

	return cores, memory, disk
}

func (pc *ProxmoxClient) UpdateVMStatus(vmName, nodeName string) (*proxmoxv1alpha1.QEMUStatus, error) {
	var VirtualMachineIP string
	var VirtualMachineOS string
	var VirtualmachineStatus *proxmoxv1alpha1.QEMUStatus
	// Get VM status
	// Check if VM is already created
	vmExists, err := pc.CheckVM(vmName, nodeName)
	if err != nil {
		return nil, err
	}
	if vmExists {
		// Get VMID
		VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
		if err != nil {
			return nil, err
		}
		agentRunning, err := pc.AgentIsRunning(vmName, nodeName)
		if err != nil {
			log.Log.Error(err, "Error checking if agent is running")
			return nil, err
		}
		if agentRunning {
			VirtualMachineIP = pc.GetVMIPAddress(vmName, nodeName)
			VirtualMachineOS = pc.GetOSInfo(vmName, nodeName)
		} else {
			VirtualMachineIP = "nil"
			VirtualMachineOS = "nil"
		}
		VirtualmachineStatus = &proxmoxv1alpha1.QEMUStatus{
			State:     VirtualMachine.Status,
			ID:        int(VirtualMachine.VMID),
			Node:      VirtualMachine.Node,
			Uptime:    pc.GetVMUptime(vmName, nodeName),
			IPAddress: VirtualMachineIP,
			OSInfo:    VirtualMachineOS,
		}
		return VirtualmachineStatus, nil
	} else {
		VirtualmachineStatus = &proxmoxv1alpha1.QEMUStatus{
			State:     "nil",
			ID:        0,
			Node:      "nil",
			Uptime:    "nil",
			IPAddress: "nil",
			OSInfo:    "nil",
		}
		return VirtualmachineStatus, nil
	}
}

func (pc *ProxmoxClient) UpdateVM(vm *proxmoxv1alpha1.VirtualMachine) (bool, error) {
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName
	updateStatus := false
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		return false, err
	}
	// Update VM
	desiredCores := GetCores(vm)
	desiredMemory := GetMemory(vm)
	VirtualMachineMem := VirtualMachine.MaxMem / 1024 / 1024 // As MB

	cpuOption := proxmox.VirtualMachineOption{
		Name:  virtualMachineCPUOption,
		Value: desiredCores,
	}
	memoryOption := proxmox.VirtualMachineOption{
		Name:  virtualMachineMemoryOption,
		Value: desiredMemory,
	}

	if VirtualMachine.CPUs != desiredCores || VirtualMachineMem != uint64(desiredMemory) {
		var task *proxmox.Task
		task, err = VirtualMachine.Config(ctx, cpuOption, memoryOption)
		if err != nil {
			log.Log.Error(err, "Can't update VM")
			return false, err
		}

		taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
			virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
		if !taskStatus {
			// Return the taks.ExitStatus as error
			return false, &TaskError{ExitStatus: task.ExitStatus}
		}
		if !taskCompleted {
			return false, taskErr
		}
		// After config update, restart VM
		if VirtualMachine.Status != VirtualMachineRunningState {
			// If the machine is not running then return
			return true, nil
		}

		task, err = pc.RestartVM(vmName, nodeName)
		if err != nil {
			return false, err
		}
		taskStatus, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx,
			virtualMachineRestartTimesNum, virtualMachineRestartSteps)
		if !taskStatus {
			// Return the taks.ExitStatus as error
			return false, &TaskError{ExitStatus: task.ExitStatus}
		}
		if !taskCompleted {
			log.Log.Error(taskErr, "Can't restart VM")
			return false, taskErr
		} else {
			updateStatus = true
		}
	}
	return updateStatus, nil
}

func (pc *ProxmoxClient) CreateManagedVM(managedVM string) (*proxmoxv1alpha1.ManagedVirtualMachine, error) {
	nodeName, err := pc.GetNodeOfVM(managedVM)
	if err != nil {
		return nil, err
	}
	cores, memory, disk := pc.GetManagedVMSpec(managedVM, nodeName)

	// Create VM object
	VirtualMachine := &proxmoxv1alpha1.ManagedVirtualMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "proxmox.alperen.cloud/v1alpha1",
			Kind:       "ManagedVirtualMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.ToLower(managedVM),
		},
		Spec: proxmoxv1alpha1.ManagedVirtualMachineSpec{
			Name:     managedVM,
			NodeName: nodeName,
			Cores:    cores,
			Memory:   memory,
			Disk:     disk,
		},
	}
	return VirtualMachine, err
}

func (pc *ProxmoxClient) GetManagedVMs() ([]string, error) {
	// Get VMs with tag managedVirtualMachineTag
	nodes, err := pc.GetOnlineNodes()
	if err != nil {
		return nil, err
	}
	var ManagedVMs []string
	for _, nodeName := range nodes {
		node, err := pc.getNode(ctx, nodeName)
		if err != nil {
			return nil, err
		}
		VirtualMachines, err := node.VirtualMachines(ctx)
		if err != nil {
			return nil, err
		}
		for _, VirtualMachine := range VirtualMachines {
			vmTags := strings.Split(VirtualMachine.Tags, ";")
			// Check if VM has managedVirtualMachineTag but not kubemox tag
			if utils.StringInSlice(ManagedVirtualMachineTag, vmTags) && !utils.StringInSlice(virtualMachineTag, vmTags) {
				ManagedVMs = append(ManagedVMs, VirtualMachine.Name)
				// Cache the VM ID
				pc.setCachedVMID(nodeName, VirtualMachine.Name, int(VirtualMachine.VMID))
			}
		}
	}
	return ManagedVMs, nil
}

func (pc *ProxmoxClient) UpdateManagedVM(ctx context.Context, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) error {
	managedVMName := managedVM.Spec.Name
	nodeName, err := pc.GetNodeOfVM(managedVMName)
	if err != nil {
		return err
	}
	vmState, err := pc.GetVMState(managedVMName, nodeName)
	if err != nil {
		return err
	}
	if vmState != VirtualMachineRunningState {
		return fmt.Errorf("managed virtual machine %s is not running, update can't be applied", managedVMName)
	} else {
		VirtualMachine, err := pc.getVirtualMachine(managedVMName, nodeName)
		if err != nil {
			return err
		}
		VirtualMachineMem := VirtualMachine.MaxMem / 1024 / 1024 // As MB
		var cpuOption proxmox.VirtualMachineOption
		var memoryOption proxmox.VirtualMachineOption
		cpuOption.Name = virtualMachineCPUOption
		cpuOption.Value = managedVM.Spec.Cores
		memoryOption.Name = virtualMachineMemoryOption
		memoryOption.Value = managedVM.Spec.Memory
		// Disk
		diskSize := managedVM.Spec.Disk
		// TODO: Need to retrieve disk name from external resource
		disk := "scsi0"
		VirtualMachineMaxDisk := VirtualMachine.MaxDisk / 1024 / 1024 / 1024 // As GB
		// convert string to uint64
		if VirtualMachineMaxDisk <= uint64(diskSize) {
			// Resize Disk
			err = VirtualMachine.ResizeDisk(ctx, disk, strconv.Itoa(diskSize)+"G")
			if err != nil {
				log.Log.Error(err, "Can't resize disk")
			}
		} else {
			log.Log.Info(fmt.Sprintf("External resource: %d || Custom Resource: %d", VirtualMachineMaxDisk, diskSize))
			log.Log.Info(fmt.Sprintf("VirtualMachine %s disk %s can't shrink.", managedVMName, disk))
			// Revert the update since it's not possible to shrink disk
			managedVM.Spec.Disk = int(VirtualMachineMaxDisk)
		}

		if VirtualMachine.CPUs != managedVM.Spec.Cores || VirtualMachineMem != uint64(managedVM.Spec.Memory) {
			// Update VM
			// log.Log.Info(fmt.Sprintf("The comparison between CR and external resource: CPU: %d, %d
			// || Memory: %d, %d", managedVM.Spec.Cores, VirtualMachine.CPUs, managedVM.Spec.Memory, VirtualMachineMem))
			task, err := VirtualMachine.Config(ctx, cpuOption, memoryOption)
			if err != nil {
				log.Log.Error(err, "Can't update VM")
				return err
			}
			taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx,
				virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
			if !taskStatus {
				// Return the taks.ExitStatus as error
				return &TaskError{ExitStatus: task.ExitStatus}
			}
			if !taskCompleted {
				return taskErr
			}
			task, err = pc.RestartVM(managedVMName, nodeName)
			if err != nil {
				return err
			}
			taskStatus, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx,
				virtualMachineRestartTimesNum, virtualMachineRestartSteps)
			if !taskStatus {
				// Return the taks.ExitStatus as error
				return &TaskError{ExitStatus: task.ExitStatus}
			}
			if !taskCompleted {
				return taskErr
			}
		}
	}
	return nil
}

func (pc *ProxmoxClient) CreateVMSnapshot(vmName, snapshotName string) (statusCode int, err error) {
	nodeName, err := pc.GetNodeOfVM(vmName)
	if err != nil {
		log.Log.Error(err, "Error getting node of VM for snapshot creation")
		return 1, err
	}
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for snapshot creation")
		return 1, err
	}
	// Create snapshot
	task, err := VirtualMachine.NewSnapshot(ctx, snapshotName)
	if err != nil {
		return 1, err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return 1, &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't create snapshot for the VirtualMachine %s", vmName)
		return 1, taskErr
	}
	return 0, nil
}

func (pc *ProxmoxClient) GetVMSnapshots(vmName string) ([]string, error) {
	nodeName, err := pc.GetNodeOfVM(vmName)
	if err != nil {
		log.Log.Error(err, "Error getting node of VM for snapshot listing")
	}
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for snapshot listing")
	}
	// Get snapshots
	snapshots, err := VirtualMachine.Snapshots(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting snapshots")
	}
	snapshotNames := []string{}
	for _, snapshot := range snapshots {
		snapshotNames = append(snapshotNames, snapshot.Name)
	}
	return snapshotNames, err
}

func (pc *ProxmoxClient) VMSnapshotExists(vmName, snapshotName string) bool {
	snapshots, err := pc.GetVMSnapshots(vmName)
	if err != nil {
		log.Log.Error(err, "Error getting snapshots")
		return false
	}
	for _, snapshot := range snapshots {
		if strings.EqualFold(snapshot, snapshotName) {
			return true
		}
	}
	return false
}

func (pc *ProxmoxClient) RemoveVirtualMachineTag(vmName, nodeName, tag string) error {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for removing tag")
	}
	removeTagTask, err := VirtualMachine.RemoveTag(ctx, tag)
	if err != nil {
		return err
	}
	taskStatus, taskCompleted, taskErr := removeTagTask.WaitForCompleteStatus(ctx, 5, 3)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: removeTagTask.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Error removing tag from VirtualMachine")
		return taskErr
	}
	return nil
}

func (pc *ProxmoxClient) GetNetworkConfiguration(vm *proxmoxv1alpha1.VirtualMachine) (map[string]string, error) {
	VirtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		return make(map[string]string), err
	}
	// Get all networks of VM
	return VirtualMachine.VirtualMachineConfig.MergeNets(), nil
}

func parseNetworkConfiguration(networks map[string]string) ([]proxmoxv1alpha1.VirtualMachineNetwork, error) {
	networkConfiguration := []proxmoxv1alpha1.VirtualMachineNetwork{}

	// Parse networks to use as VirtualMachineSpecTemplateNetwork
	for _, network := range networks {
		networkSplit := strings.Split(network, ",")
		if len(networkSplit) < 2 {
			return nil, fmt.Errorf("invalid format for network configuration: %s", network)
		}
		// Get the network model name
		networkModel := strings.Split(networkSplit[0], "=")[0] // The key of first value
		// Get the network bridge name
		networkBridge := strings.Split(networkSplit[1], "=")[1] // The value of second value
		networkConfiguration = append(networkConfiguration, proxmoxv1alpha1.VirtualMachineNetwork{
			Model:  networkModel,
			Bridge: networkBridge,
		})
	}
	return networkConfiguration, nil
}

func (pc *ProxmoxClient) ConfigureVirtualMachine(vm *proxmoxv1alpha1.VirtualMachine) error {
	err := pc.configureVirtualMachineNetwork(vm)
	if err != nil {
		return err
	}
	err = pc.configureVirtualMachineDisk(vm)
	if err != nil {
		return err
	}

	err = pc.configureVirtualMachinePCI(vm)
	if err != nil {
		return err
	}
	return nil
}

func (pc *ProxmoxClient) deleteVirtualMachineOption(vm *proxmoxv1alpha1.VirtualMachine,
	option string) (proxmox.Task, error) {
	nodeName := vm.Spec.NodeName
	virtualMachine, err := pc.getVirtualMachine(vm.Name, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for deleting option")
	}
	// Delete option
	taskID, err := virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "delete",
		Value: option,
	})
	return *taskID, err
}

func (pc *ProxmoxClient) updateNetworkConfig(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine, i int, networks []proxmoxv1alpha1.VirtualMachineNetwork) error {
	// Get the network model&bridge name
	networkModel := networks[i].Model
	networkBridge := networks[i].Bridge
	// Update the network configuration
	virtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating network configuration")
	}
	task, err := virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  netStr + strconv.Itoa(i),
		Value: networkModel + "," + "bridge=" + networkBridge,
	})
	if err != nil {
		return err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Error updating network configuration")
		return taskErr
	}
	return nil
}

// func configureVirtualMachineNetwork(vm *proxmoxv1alpha1.VirtualMachine) error {
// // Get desired network configuration
// networks := vm.Spec.Template.Network
// // Get actual network configuration
// virtualMachineNetworks, err := GetNetworkConfiguration(vm)
// if err != nil {
// return err
// }
// log.Log.Info(fmt.Sprintf("Actual network configuration before parser: %v", virtualMachineNetworks))

// // Parse actual network configuration
// virtualMachineNetworksParsed, err := parseNetworkConfiguration(virtualMachineNetworks)
// if err != nil {
// return err
// }
// // DEBUG
// log.Log.Info(fmt.Sprintf("Configuring network for VirtualMachine %s", vm.Name))

// log.Log.Info(fmt.Sprintf("Desired network configuration: %v", *networks))

// log.Log.Info(fmt.Sprintf("Actual network configuration: %v", virtualMachineNetworksParsed))

// // Classify network configurations
// networksToAdd, networksToUpdate, networksToDelete := classifyNetworks(*networks, virtualMachineNetworksParsed)

// // DEBUG
// log.Log.Info(fmt.Sprintf("Networks to add: %v", networksToAdd))
// log.Log.Info(fmt.Sprintf("Networks to update: %v", networksToUpdate))
// log.Log.Info(fmt.Sprintf("Networks to delete: %v", networksToDelete))

// // Apply network changes
// if err := applyNetworkChanges(ctx, vm, networksToAdd, networksToUpdate, networksToDelete); err != nil {
// return err
// }
// return nil
// }

// func classifyNetworks(desiredNetworks, actualNetworks []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) (
// networksToAdd, networksToUpdate, networksToDelete []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) {
// getKey := func(network proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) string {
// return "net" + getNetworkIndex(desiredNetworks, &network)
// }
// return classifyItems(desiredNetworks, actualNetworks, getKey)
// }

// func applyNetworkChanges(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
// networksToAdd, networksToUpdate, networksToDelete []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) error {
// getModel := func(network proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) string {
// return "net" + getNetworkIndex(*vm.Spec.Template.Network, &network)
// }
// return applyChanges(ctx, vm, networksToAdd, networksToUpdate,
// networksToDelete, getModel, addNetworkConfig, updateNetworkConfig, "Network")
// }

// func addNetworkConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
// network proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) error {
// // Add the network configuration
// virtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
// if err != nil {
// log.Log.Error(err, "Error getting VM for adding network configuration")
// }
// _, err = virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
// Name:  "net" + getNetworkIndex(*vm.Spec.Template.Network, &network),
// Value: network.Model + "," + "bridge=" + network.Bridge,
// })
// return err
// }

func (pc *ProxmoxClient) configureVirtualMachineNetwork(vm *proxmoxv1alpha1.VirtualMachine) error {
	// Get desired network configuration
	networks := getNetworks(vm)
	// Get actual network configuration
	virtualMachineNetworks, err := pc.GetNetworkConfiguration(vm)
	if err != nil {
		return err
	}
	virtualMachineNetworksParsed, err := parseNetworkConfiguration(virtualMachineNetworks)
	if err != nil {
		return err
	}
	// Check if network configuration is different
	if !cmp.Equal(networks, virtualMachineNetworksParsed) {
		// The desired network configuration is different than the actual one
		log.Log.Info(fmt.Sprintf("Updating network configuration for VirtualMachine %s", vm.Name))
		// Update the network configuration
		for i := len(networks); i < len(virtualMachineNetworksParsed); i++ {
			// Remove the network configuration
			log.Log.Info(fmt.Sprintf("Removing the network configuration for net%d of VM %s", i, vm.Spec.Name))
			var taskID proxmox.Task
			taskID, err = pc.deleteVirtualMachineOption(vm, "net"+strconv.Itoa(i))
			if err != nil {
				return err
			}
			taskStatus, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 3)
			if !taskStatus {
				// Return the task.ExitStatus as error
				return &TaskError{ExitStatus: taskID.ExitStatus}
			}
			if !taskCompleted {
				log.Log.Error(taskErr, "Error removing network configuration from VirtualMachine")
			}
		}
		for i := len(virtualMachineNetworksParsed); i < len(networks); i++ {
			// Add the network configuration
			log.Log.Info(fmt.Sprintf("Adding the network configuration for net%d of VM %s", i, vm.Spec.Name))
			err = pc.updateNetworkConfig(ctx, vm, i, networks)
			if err != nil {
				return err
			}
		}
		for i := 0; i < len(virtualMachineNetworksParsed); i++ {
			// Check if the network configuration is different
			if len(networks) != 0 && !cmp.Equal(networks[i], virtualMachineNetworksParsed[i]) {
				// Update the network configuration
				log.Log.Info(fmt.Sprintf("Updating the network configuration for net%d of VM %s", i, vm.Spec.Name))
				// Get the network model&bridge name
				err = pc.updateNetworkConfig(ctx, vm, i, networks)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (pc *ProxmoxClient) GetDiskConfiguration(vm *proxmoxv1alpha1.VirtualMachine) (map[string]string, error) {
	nodeName := vm.Spec.NodeName
	VirtualMachine, err := pc.getVirtualMachine(vm.Name, nodeName)
	if err != nil {
		return make(map[string]string), err
	}
	disks := VirtualMachine.VirtualMachineConfig.MergeDisks()
	// Remove entries with media=cdrom
	for key, value := range disks {
		if strings.Contains(value, "media=cdrom") {
			delete(disks, key)
		}
	}
	return disks, nil
}

func (pc *ProxmoxClient) configureVirtualMachineDisk(vm *proxmoxv1alpha1.VirtualMachine) error {
	// Get VM disk spec and actual disk configuration
	disks := getDisks(vm)
	virtualMachineDisks, err := pc.GetDiskConfiguration(vm)
	if err != nil {
		return err
	}
	virtualMachineDisksParsed, err := parseDiskConfiguration(virtualMachineDisks)
	if err != nil {
		return err
	}

	// Classify disk configurations
	disksToAdd, disksToUpdate, disksToDelete := classifyDisks(disks, virtualMachineDisksParsed)

	// Apply disk changes
	if err := pc.applyDiskChanges(ctx, vm, disksToAdd, disksToUpdate, disksToDelete); err != nil {
		return err
	}
	return nil
}

func classifyDisks(desiredDisks, actualDisks []proxmoxv1alpha1.VirtualMachineDisk) (
	disksToAdd, disksToUpdate, disksToDelete []proxmoxv1alpha1.VirtualMachineDisk) {
	getKey := func(disk proxmoxv1alpha1.VirtualMachineDisk) string {
		return disk.Device
	}
	return classifyItems(desiredDisks, actualDisks, getKey)
}

func (pc *ProxmoxClient) applyDiskChanges(
	ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine,
	disksToAdd, disksToUpdate, disksToDelete []proxmoxv1alpha1.VirtualMachineDisk) error {
	getDeviceID := func(disk proxmoxv1alpha1.VirtualMachineDisk) string {
		return disk.Device
	}

	return applyChanges(
		pc,
		ctx,
		vm,
		disksToAdd,
		disksToUpdate,
		disksToDelete,
		getDeviceID,
		pc.addDiskConfig,
		pc.updateDiskConfig,
		"Disk",
	)
}

func parseDiskConfiguration(disks map[string]string) ([]proxmoxv1alpha1.VirtualMachineDisk, error) {
	diskConfiguration := []proxmoxv1alpha1.VirtualMachineDisk{}

	// Parse disks to use as VirtualMachineDisk
	for device, disk := range disks {
		diskSplit := strings.Split(disk, ",")
		if len(diskSplit) < 2 {
			return nil, fmt.Errorf("invalid format for disk configuration: %s", disk)
		}
		// Get the disk storage name
		diskStorage := strings.Split(diskSplit[0], ":")[0] // The key of first value
		// Get the disk size from the key = size
		var diskSize int
		var err error
		for _, part := range diskSplit {
			if strings.Contains(part, "size") {
				sizeStr := strings.Split(part, "=")[1]
				if strings.HasSuffix(sizeStr, "G") {
					trimmed := strings.TrimSuffix(sizeStr, "G")
					diskSize, err = strconv.Atoi(trimmed)
					if err != nil {
						return nil, err
					}
				} else if strings.HasSuffix(sizeStr, "M") {
					trimmed := strings.TrimSuffix(sizeStr, "M")
					sizeMB, err := strconv.Atoi(trimmed)
					if err != nil {
						return nil, err
					}
					// Convert MB to GB for disk size
					diskSize = sizeMB / 1024
				}
				break
			}
		}
		diskConfiguration = append(diskConfiguration, proxmoxv1alpha1.VirtualMachineDisk{
			Storage: diskStorage,
			Size:    diskSize,
			Device:  device,
		})
	}
	return diskConfiguration, nil
}

func (pc *ProxmoxClient) updateDiskConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	disk proxmoxv1alpha1.VirtualMachineDisk) error {
	logger := log.FromContext(ctx)
	// Get actual disk config to compare storage
	actualDisksMap, err := pc.GetDiskConfiguration(vm)
	if err != nil {
		logger.Error(err, "Error getting actual disk configuration")
		return err
	}
	actualDisks, err := parseDiskConfiguration(actualDisksMap)
	if err != nil {
		logger.Error(err, "Error parsing actual disk configuration")
		return err
	}

	var actualDiskStorage string
	for _, actualDisk := range actualDisks {
		if actualDisk.Device == disk.Device {
			actualDiskStorage = actualDisk.Storage
			break
		}
	}

	// If storage is different, log it and do not update the disk
	if actualDiskStorage != "" && actualDiskStorage != disk.Storage {
		logger.Info(fmt.Sprintf("Storage for disk %s cannot be changed from %s to %s, skipping update for this disk",
			disk.Device, actualDiskStorage, disk.Storage))
		return nil
	}

	virtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		logger.Error(err, "Error getting VM for updating disk configuration")
		return err
	}
	err = virtualMachine.ResizeDisk(ctx, disk.Device, strconv.Itoa(disk.Size)+"G")
	if err != nil {
		log.Log.Error(err, "Error updating disk configuration for VirtualMachine")
		return err
	}
	logger.Info(fmt.Sprintf("Disk %s updated to size %dG on storage %s", disk.Device, disk.Size, disk.Storage))
	return nil
}

func (pc *ProxmoxClient) addDiskConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	disk proxmoxv1alpha1.VirtualMachineDisk) error {
	virtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating disk configuration")
		return err
	}
	taskID, err := virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  disk.Device,
		Value: disk.Storage + ":" + strconv.Itoa(disk.Size),
	})
	if err != nil {
		log.Log.Error(err, "Error adding disk configuration for VirtualMachine")
		return err
	}
	taskStatus, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 3)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: taskID.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Error updating disk configuration for VirtualMachine")
		return nil
	}
	return nil
}

func (pc *ProxmoxClient) CheckVirtualMachineDelta(vm *proxmoxv1alpha1.VirtualMachine) (bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
		// Return here to avoid further processing if VM is not found
		return false, err
	}
	// Get actual VM's network configuration
	virtualMachineNetworks, err := pc.GetNetworkConfiguration(vm)
	if err != nil {
		return false, err
	}
	VirtualMachineNetworksParsed, err := parseNetworkConfiguration(virtualMachineNetworks)
	if err != nil {
		return false, err
	}
	virtualMachineDisks, err := pc.GetDiskConfiguration(vm)
	if err != nil {
		return false, err
	}
	VirtualMachineDisksParsed, err := parseDiskConfiguration(virtualMachineDisks)
	if err != nil {
		return false, err
	}

	VirtualMachineConfig := VirtualMachine.VirtualMachineConfig
	actualVM := VirtualMachineComparison{
		Cores:    VirtualMachineConfig.Cores,
		Sockets:  VirtualMachineConfig.Sockets,
		Memory:   int(VirtualMachineConfig.Memory),
		Networks: VirtualMachineNetworksParsed,
		Disks:    sortDisks(VirtualMachineDisksParsed),
	}
	// Desired VM
	desiredVM := VirtualMachineComparison{
		Cores:    GetCores(vm),
		Sockets:  getSockets(vm),
		Memory:   GetMemory(vm),
		Networks: getNetworks(vm),
		Disks:    sortDisks(getDisks(vm)),
	}
	// Compare the actual VM with the desired VM
	if !cmp.Equal(actualVM, desiredVM) {
		return true, nil
	}
	return false, nil
}

func sortDisks(disks []proxmoxv1alpha1.VirtualMachineDisk) []proxmoxv1alpha1.VirtualMachineDisk {
	sort.Slice(disks, func(i, j int) bool {
		if disks[i].Storage == disks[j].Storage {
			return disks[i].Device < disks[j].Device
		}
		return disks[i].Storage < disks[j].Storage
	})
	return disks
}

func (pc *ProxmoxClient) CheckManagedVMDelta(managedVM *proxmoxv1alpha1.ManagedVirtualMachine) (
	bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := pc.getVirtualMachine(managedVM.Spec.Name, managedVM.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
		return false, err
	}
	VirtualMachineConfig := VirtualMachine.VirtualMachineConfig

	// Compare the actual VM with the desired VM
	if VirtualMachineConfig.Cores != managedVM.Spec.Cores || int(VirtualMachineConfig.Memory) != managedVM.Spec.Memory {
		return true, nil
	}
	return false, nil
}

func getNextVMID(client *proxmox.Client) (int, error) {
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return 0, err
	}
	vmID, err := cluster.NextID(ctx)
	if err != nil {
		return 0, err
	}
	return vmID, nil
}

func (pc *ProxmoxClient) getVirtualMachine(vmName, nodeName string) (*proxmox.VirtualMachine, error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	vmID, err := pc.getVMID(vmName, nodeName)
	if err != nil {
		return nil, err
	}
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return nil, err
	}
	return VirtualMachine, nil
}

func (pc *ProxmoxClient) configureVirtualMachinePCI(vm *proxmoxv1alpha1.VirtualMachine) error {
	desiredPCIs := getPciDevices(vm)
	actualPCIsMap, err := pc.GetPCIConfiguration(vm.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	actualPCIs := make(map[string]proxmoxv1alpha1.PciDevice)
	for key, pciString := range actualPCIsMap {
		pci, err := parseSinglePCIConfiguration(pciString)
		if err != nil {
			log.Log.Error(err, "error parsing PCI configuration, skipping", "pciString", pciString)
			continue
		}
		actualPCIs[key] = pci
	}
	// Apply PCI changes
	if len(desiredPCIs) > 0 {
		reboot, err := pc.ApplyPCIChanges(vm, desiredPCIs, actualPCIs)
		if err != nil {
			return err
		}
		if reboot {
			// Rebooting VM spawns two different tasks, one for stopping and one for starting
			// and unfortunately you can't track the start so
			// here we should do stop and start separately
			// Stop VM
			err = pc.StopVM(vm.Name, vm.Spec.NodeName)
			if err != nil {
				log.Log.Error(err, "Error stopping VirtualMachine")
				return err
			}
			// TODO: Implement something more logical
			// Start VM
			VirtualMachine, err := pc.getVirtualMachine(vm.Name, vm.Spec.NodeName)
			if err != nil {
				log.Log.Error(err, "Error getting VM")
				return err
			}
			task, err := VirtualMachine.Start(ctx)
			if err != nil {
				log.Log.Error(err, "Error starting VirtualMachine")
				return err
			}
			taskStatus, taskCompleted, err := task.WaitForCompleteStatus(ctx, 5, 3)
			if err != nil {
				log.Log.Error(err, "Error starting VirtualMachine")
				return err
			}
			if !taskStatus {
				return &TaskError{ExitStatus: task.ExitStatus}
			}
			if !taskCompleted {
				return err
			}
		}
	}

	return nil
}

func (pc *ProxmoxClient) GetPCIConfiguration(vmName, nodeName string) (map[string]string, error) {
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		return make(map[string]string), err
	}
	PCIs := VirtualMachine.VirtualMachineConfig.MergeHostPCIs()
	return PCIs, nil
}

func (pc *ProxmoxClient) ApplyPCIChanges(vm *proxmoxv1alpha1.VirtualMachine, desiredPCIs []proxmoxv1alpha1.PciDevice,
	actualPCIs map[string]proxmoxv1alpha1.PciDevice) (bool, error) {
	reboot := false
	for i, desiredPCI := range desiredPCIs {
		indexStr := fmt.Sprintf("hostpci%d", i)
		if actualPCI, ok := actualPCIs[indexStr]; ok {
			// Device exists, check if it needs update
			if !reflect.DeepEqual(actualPCI, desiredPCI) {
				log.Log.Info(fmt.Sprintf("Updating PCI device %s for VirtualMachine %s", indexStr, vm.Name))
				if err := pc.updatePCIConfig(vm, indexStr, desiredPCI); err != nil {
					return false, err
				}
				reboot = true
			}
			delete(actualPCIs, indexStr)
		} else {
			// Device does not exist, add it
			log.Log.Info(fmt.Sprintf("Adding PCI device %s to VirtualMachine %s", desiredPCI.DeviceID, vm.Name))
			if err := pc.updatePCIConfig(vm, indexStr, desiredPCI); err != nil {
				return false, err
			}
			reboot = true
		}
	}

	// Delete any remaining actual PCI devices that are not in the desired spec
	for indexStr := range actualPCIs {
		log.Log.Info(fmt.Sprintf("Deleting PCI device %s of VirtualMachine %s", indexStr, vm.Name))
		task, err := pc.deleteVirtualMachineOption(vm, indexStr)
		if err != nil {
			return false, err
		}
		taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
		if !taskStatus {
			return false, &TaskError{ExitStatus: task.ExitStatus}
		}
		if !taskCompleted {
			return false, taskErr
		}
		reboot = true
	}
	return reboot, nil
}

func (pc *ProxmoxClient) updatePCIConfig(vm *proxmoxv1alpha1.VirtualMachine, index string,
	pci proxmoxv1alpha1.PciDevice) error {
	vmName, nodeName := vm.Name, vm.Spec.NodeName
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
		return err
	}

	task, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  index,
		Value: buildPCIOptions(pci),
	})
	if err != nil {
		log.Log.Error(err, "Error updating PCI configuration for VirtualMachine")
		return err
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
	if !taskStatus {
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskErr
	}
	return nil
}

func parseSinglePCIConfiguration(pci string) (proxmoxv1alpha1.PciDevice, error) {
	var pciConfig proxmoxv1alpha1.PciDevice
	pciSplit := strings.Split(pci, ",")

	// Check for mapped device
	if strings.HasPrefix(pciSplit[0], "mapping=") {
		pciConfig.Type = "mapped"
		pciConfig.DeviceID = strings.TrimPrefix(pciSplit[0], "mapping=")
	} else {
		pciConfig.Type = "raw"
		pciConfig.DeviceID = pciSplit[0]
	}

	for _, pciSplit := range pciSplit[1:] {
		kv := strings.SplitN(pciSplit, "=", 2)
		if len(kv) != 2 {
			return proxmoxv1alpha1.PciDevice{}, fmt.Errorf("invalid format for PCI configuration: %s", pciSplit)
		}
		key := kv[0]

		// Check for the PCIE and x-vga keys
		switch key {
		case "pcie":
			pciConfig.PCIE = true
		case "x-vga":
			pciConfig.PrimaryGPU = true
		}
	}
	return pciConfig, nil
}

func buildPCIOptions(pci proxmoxv1alpha1.PciDevice) string {
	var pciOptions []string
	if pci.Type == "mapped" {
		pciOptions = append(pciOptions, "mapping="+pci.DeviceID)
	} else {
		pciOptions = append(pciOptions, pci.DeviceID)
	}
	if pci.PCIE {
		pciOptions = append(pciOptions, "pcie=1")
	}
	if pci.PrimaryGPU {
		pciOptions = append(pciOptions, "x-vga=1")
	}
	// Join only if there are extra pciOptions beyond the device ID or mapping
	return strings.Join(pciOptions, ",")
}

// func revertVirtualMachineOption(vmName, nodeName, value string) error {
// 	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
// 	if err != nil {
// 		log.Log.Error(err, "Error getting VM for reverting")
// 	}
// 	revertTask, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
// 		Name:  "revert",
// 		Value: value,
// 	})
// 	if err != nil {
// 		log.Log.Error(err, "Error reverting VirtualMachine")
// 	}
// 	taskStatus, taskCompleted, taskErr := revertTask.WaitForCompleteStatus(ctx, 5, 3)
// 	if !taskStatus {
// 		// Return the task.ExitStatus as error
// 		return &TaskError{ExitStatus: revertTask.ExitStatus}
// 	}
// 	if !taskCompleted {
// 		log.Log.Error(taskErr, "Error occurred while reverting VirtualMachine")
// 		return taskErr
// 	}
// 	return nil
// }

func (pc *ProxmoxClient) RebootVM(vmName, nodeName string) error {
	virtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for rebooting")
	}
	// Reboot VM
	task, err := virtualMachine.Reboot(ctx)
	if err != nil {
		log.Log.Error(err, "Error rebooting VirtualMachine %s", vmName)
	}
	taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
	if !taskStatus {
		// Return the task.ExitStatus as error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		log.Log.Error(taskErr, "Error rebooting VirtualMachine")
		return taskErr
	}
	return nil
}

func (pc *ProxmoxClient) ApplyAdditionalConfiguration(vm Resource) error {
	var vmName, nodeName string
	var additionalConfig map[string]string
	// vm could be either VirtualMachine or VirtualMachineTemplate
	if vm.GetObjectKind().GroupVersionKind().Kind == "VirtualMachine" {
		vmName = vm.(*proxmoxv1alpha1.VirtualMachine).Spec.Name
		nodeName = vm.(*proxmoxv1alpha1.VirtualMachine).Spec.NodeName
		additionalConfig = vm.(*proxmoxv1alpha1.VirtualMachine).Spec.AdditionalConfig
	} else if vm.GetObjectKind().GroupVersionKind().Kind == "VirtualMachineTemplate" {
		vmName = vm.(*proxmoxv1alpha1.VirtualMachineTemplate).Spec.Name
		nodeName = vm.(*proxmoxv1alpha1.VirtualMachineTemplate).Spec.NodeName
		additionalConfig = vm.(*proxmoxv1alpha1.VirtualMachineTemplate).Spec.AdditionalConfig
	}

	// Get VirtualMachine
	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for applying additional configuration")
		return err
	}
	// Apply additional configuration
	for key, value := range additionalConfig {
		var task *proxmox.Task
		task, err = VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
			Name:  key,
			Value: value,
		})
		if err != nil {
			log.Log.Error(err, "Error applying additional configuration")
		}
		taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 3)
		if !taskStatus {
			// Return the task.ExitStatus as error
			return &TaskError{ExitStatus: task.ExitStatus}
		}
		if !taskCompleted {
			log.Log.Error(taskErr, "Error applying additional configuration")
			return taskErr
		}
	}
	// Check if there is a need to reboot the VM
	pendingConfig, err := VirtualMachine.Pending(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting pending configuration")
		return err
	}
	var reboot bool
	// TODO: Re-think about the watching pendingConfig
	for _, config := range *pendingConfig {
		if config.Pending != nil {
			reboot = true
			break
		}
	}
	if reboot {
		// Reboot the VM
		err = pc.RebootVM(vmName, nodeName)
		if err != nil {
			log.Log.Error(err, "Error rebooting VirtualMachine")
		}
	}
	return nil
}

func (pc *ProxmoxClient) IsVirtualMachineReady(obj Resource) (bool, error) {
	var vmName, nodeName string
	objectKind := obj.GetObjectKind()
	switch objectKind.GroupVersionKind().Kind {
	case "VirtualMachine":
		vmName = obj.(*proxmoxv1alpha1.VirtualMachine).Spec.Name
		nodeName = obj.(*proxmoxv1alpha1.VirtualMachine).Spec.NodeName
	case "ManagedVirtualMachine":
		vmName = obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.Name
		nodeName = obj.(*proxmoxv1alpha1.ManagedVirtualMachine).Spec.NodeName
	case "VirtualMachineTemplate":
		vmName = obj.(*proxmoxv1alpha1.VirtualMachineTemplate).Spec.Name
		nodeName = obj.(*proxmoxv1alpha1.VirtualMachineTemplate).Spec.NodeName
	}
	// Get VM ID
	vmID, err := pc.getVMID(vmName, nodeName)
	if err != nil {
		return false, err
	}
	if vmID == 0 {
		return false, nil
	}

	VirtualMachine, err := pc.getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for checking readiness")
		return false, err
	}

	lock := VirtualMachine.Lock
	if lock != "" {
		return false, nil
	}
	return true, nil
}

// Helper functions

func GetCores(vm *proxmoxv1alpha1.VirtualMachine) int {
	if CheckVMType(vm) == VirtualMachineTemplateType {
		return vm.Spec.Template.Cores
	}
	return vm.Spec.VMSpec.Cores
}

func GetMemory(vm *proxmoxv1alpha1.VirtualMachine) int {
	if CheckVMType(vm) == VirtualMachineTemplateType {
		return vm.Spec.Template.Memory
	}
	return vm.Spec.VMSpec.Memory
}

func getSockets(vm *proxmoxv1alpha1.VirtualMachine) int {
	if CheckVMType(vm) == VirtualMachineTemplateType {
		return vm.Spec.Template.Socket
	}
	return vm.Spec.VMSpec.Socket
}

func getDisks(vm *proxmoxv1alpha1.VirtualMachine) []proxmoxv1alpha1.VirtualMachineDisk {
	if CheckVMType(vm) == VirtualMachineTemplateType {
		return vm.Spec.Template.Disk
	}
	return vm.Spec.VMSpec.Disk
}

func getNetworks(vm *proxmoxv1alpha1.VirtualMachine) []proxmoxv1alpha1.VirtualMachineNetwork {
	var networks *[]proxmoxv1alpha1.VirtualMachineNetwork
	if CheckVMType(vm) == VirtualMachineTemplateType {
		networks = vm.Spec.Template.Network
	} else {
		networks = vm.Spec.VMSpec.Network
	}

	if networks == nil {
		return nil
	}
	return *networks
}

func getPciDevices(vm *proxmoxv1alpha1.VirtualMachine) []proxmoxv1alpha1.PciDevice {
	if CheckVMType(vm) == VirtualMachineTemplateType {
		return vm.Spec.Template.PciDevices
	}
	return vm.Spec.VMSpec.PciDevices
}
