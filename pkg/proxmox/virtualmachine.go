package proxmox

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/metrics"
	"github.com/alperencelik/kubemox/pkg/utils"
	proxmox "github.com/luthermonson/go-proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type VirtualMachineComparison struct {
	Sockets  int `json:"sockets"`
	Cores    int `json:"cores"`
	Memory   int `json:"memory"`
	Networks []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork
	Disks    []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk
}

var mutex = &sync.Mutex{}

const (
	// The tag that will be added to VMs in Proxmox cluster
	VirtualMachineRunningState = "running"
	VirtualMachineStoppedState = "stopped"
	virtualMachineTemplateType = "template"
	virtualMachineScratchType  = "scratch"
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
	virtualMachineTag        string
	ManagedVirtualMachineTag string
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
}

func CreateVMFromTemplate(vm *proxmoxv1alpha1.VirtualMachine) {
	nodeName := vm.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	templateVMName := vm.Spec.Template.Name
	templateVM, err := getVirtualMachine(templateVMName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting template VM")
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
	}
	log.Log.Info(fmt.Sprintf("New VM %s has been creating with ID: %d", vm.Name, newID))
	mutex.Unlock()
	// TODO: Implement a better way to watch the tasks.
	logChan, err := task.Watch(ctx, 0)
	if err != nil {
		panic(err)
	}
	for logEntry := range logChan {
		log.Log.Info(fmt.Sprintf("Virtual Machine %s, creation process: %s", vm.Name, logEntry))
	}
	mutex.Lock()
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineCreateTimesNum, virtualMachineCreateSteps)
	switch {
	case !taskCompleted:
		log.Log.Error(taskErr, "Error creating VM")
	case taskCompleted:
		log.Log.Info(fmt.Sprintf("VM %s has been created", vm.Name))
	default:
		log.Log.Info("VM creation task is still running")
	}

	// Add tag to VM
	VirtualMachine, err := node.VirtualMachine(ctx, newID)
	addTagTask, _ := VirtualMachine.AddTag(ctx, virtualMachineTag)
	_, taskCompleted, taskErr = addTagTask.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Error adding tag to VM")
	}
	mutex.Unlock()
	if err != nil {
		panic(err)
	}
}

func GetVMID(vmName, nodeName string) int {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	vmList, err := node.VirtualMachines(ctx)
	if err != nil {
		panic(err)
	}
	for _, vm := range vmList {
		if strings.EqualFold(vm.Name, vmName) {
			vmID := vm.VMID
			// Convert vmID to int
			vmIDInt := int(vmID)
			return vmIDInt
		}
	}
	return 0
}

func CheckVM(vmName, nodeName string) bool {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	vmList, err := node.VirtualMachines(ctx)
	if err != nil {
		panic(err)
	}
	for _, vm := range vmList {
		// if vm.Name == vmName {
		if strings.EqualFold(vm.Name, vmName) {
			return true
		}
	}
	return false
}

func GetVMIPAddress(vmName, nodeName string) string {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Get VM IP
	VirtualMachineIfaces, err := VirtualMachine.AgentGetNetworkIFaces(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting VM IP")
	}
	for _, iface := range VirtualMachineIfaces {
		for _, ip := range iface.IPAddresses {
			return ip.IPAddress
		}
	}
	return ""
}

func GetOSInfo(vmName, nodeName string) string {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Get VM OS
	VirtualMachineOS, err := VirtualMachine.AgentOsInfo(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting VM OS")
	}
	return VirtualMachineOS.PrettyName
}

func GetVMUptime(vmName, nodeName string) string {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Get VM Uptime as seconds
	VirtualMachineUptime := int(VirtualMachine.Uptime)
	// Convert seconds to format like 1d 2h 3m 4s
	uptime := utils.FormatUptime(VirtualMachineUptime)
	return uptime
}

func DeleteVM(vmName, nodeName string) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	mutex.Lock()
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	mutex.Unlock()
	// Stop VM
	vmStatus := VirtualMachine.Status
	if vmStatus == VirtualMachineRunningState {
		stopTask, stopErr := VirtualMachine.Stop(ctx)
		if stopErr != nil {
			panic(err)
		}
		_, taskCompleted, taskErr := stopTask.WaitForCompleteStatus(ctx, virtualMachineStopTimesNum, virtualMachineStopSteps)
		switch taskCompleted {
		case false:
			log.Log.Error(taskErr, "Can't stop VM")
		case true:
			log.Log.Info(fmt.Sprintf("VM %s has been stopped", vmName))
		default:
			log.Log.Info("VM is already stopped")
		}
	}
	// Delete VM
	task, err := VirtualMachine.Delete(ctx)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineDeleteTimesNum, virtualMachineDeleteSteps)
	switch {
	case !taskCompleted:
		log.Log.Error(taskErr, "Can't delete VM")
	case taskCompleted:
		log.Log.Info(fmt.Sprintf("VM %s has been deleted", vmName))
	default:
		log.Log.Info("VM is already deleted")
	}
}

func StartVM(vmName, nodeName string) (string, error) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM to start")
	}
	// Start VM
	task, err := VirtualMachine.Start(ctx)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineStartTimesNum, virtualMachineStartSteps)
	switch {
	case !taskCompleted:
		return "", taskErr
	case taskCompleted:
		return fmt.Sprintf("VirtualMachine %s has been started", vmName), nil
	default:
		return fmt.Sprintf("VirtualMachine %s is already running", vmName), nil
	}
}

func RestartVM(vmName, nodeName string) *proxmox.Task {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM to restart")
	}
	// Restart VM
	task, err := VirtualMachine.Reboot(ctx)
	if err != nil {
		panic(err)
	}
	return task
}

func StopVM(vmName, nodeName string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM to stop")
	}
	// Stop VM
	task, err := VirtualMachine.Stop(ctx)
	if err != nil {
		panic(err)
	}

	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineStopTimesNum, virtualMachineStopSteps)
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't stop VM")
	}
	return err
}

func GetVMState(vmName, nodeName string) string {
	// Gets the VMstate from Proxmox API
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	VirtualMachineState := VirtualMachine.Status
	if err != nil {
		panic(err)
	}
	switch VirtualMachineState {
	case VirtualMachineRunningState:
		return VirtualMachineRunningState
	case VirtualMachineStoppedState:
		return VirtualMachineStoppedState
	default:
		return "unknown"
	}
}

func AgentIsRunning(vmName, nodeName string) bool {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for agent check")
	}
	err = VirtualMachine.WaitForAgent(ctx, AgentTimeoutSeconds)
	if err != nil {
		return false
	} else {
		return true
	}
}

func CreateVMFromScratch(vm *proxmoxv1alpha1.VirtualMachine) {
	nodeName := vm.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	cores := vm.Spec.VMSpec.Cores
	memory := vm.Spec.VMSpec.Memory
	diskName := vm.Spec.VMSpec.Disk.Name
	diskSize := vm.Spec.VMSpec.Disk.Value
	networkName := vm.Spec.VMSpec.Network.Name
	networkValue := vm.Spec.VMSpec.Network.Value
	osName := vm.Spec.VMSpec.OSImage.Name
	osValue := vm.Spec.VMSpec.OSImage.Value

	// Create VM from scratch
	VMOptions := []proxmox.VirtualMachineOption{
		{
			Name:  virtualMachineCPUOption,
			Value: cores,
		},
		{
			Name:  virtualMachineMemoryOption,
			Value: memory,
		},
		{
			Name:  diskName,
			Value: diskSize,
		},
		{
			Name:  networkName,
			Value: networkValue,
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
	// Get next VMID
	vmID, err := getNextVMID(Client)
	if err != nil {
		log.Log.Error(err, "Error getting next VMID")
	}
	// Create VM
	task, err := node.NewVirtualMachine(ctx, vmID, VMOptions...)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 10, 10)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't create VM")
	case true:
		log.Log.Info(fmt.Sprintf("VM %s has been created", vm.Spec.Name))
	default:
		log.Log.Info("VM is already created")
	}
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		panic(err)
	}
	addTagTask, err := VirtualMachine.AddTag(ctx, virtualMachineTag)
	_, taskCompleted, taskErr = addTagTask.WaitForCompleteStatus(ctx, 1, 10)
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't add tag to VM")
	}
	if err != nil {
		log.Log.Error(taskErr, "Can't add tag to VM")
	}
}

func CheckVMType(vm *proxmoxv1alpha1.VirtualMachine) string {
	var VMType string
	switch {
	case !reflect.ValueOf(vm.Spec.Template).IsZero():
		VMType = virtualMachineTemplateType
	case !reflect.ValueOf(vm.Spec.VMSpec).IsZero():
		VMType = virtualMachineScratchType
	case !reflect.ValueOf(vm.Spec.Template).IsZero() && !reflect.ValueOf(vm.Spec.VMSpec).IsZero():
		VMType = "faulty"
	default:
		VMType = "undefined"
	}
	return VMType
}

func CheckManagedVMExists(managedVM string) bool {
	var existingManagedVMNames []string
	// Get managed VMs
	crd := kubernetes.GetManagedVMCRD()
	customResource := schema.GroupVersionResource{
		Group:    crd.Spec.Group,
		Version:  crd.Spec.Versions[0].Name,
		Resource: crd.Spec.Names.Plural,
	}
	// Get managedVirtualMachine CRD
	ClientManagedVMs, err := kubernetes.DynamicClient.Resource(customResource).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Log.Error(err, "Error getting managed VMs")
	}
	// Get all managed VM names as array
	for _, ClientManagedVM := range ClientManagedVMs.Items {
		existingManagedVMNames = append(existingManagedVMNames, ClientManagedVM.GetName())
	}
	// Check if managed VM exists
	return utils.StringInSlice(managedVM, existingManagedVMNames)
}

func GetManagedVMSpec(managedVMName, nodeName string) (cores, memory, disk int) {
	// Get spec of VM
	VirtualMachine, err := getVirtualMachine(managedVMName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for managed VM spec")
	}
	cores = VirtualMachine.CPUs
	memory = int(VirtualMachine.MaxMem / 1024 / 1024) // As MB
	disk = int(VirtualMachine.MaxDisk / 1024 / 1024 / 1024)

	return cores, memory, disk
}

func UpdateVMStatus(vmName, nodeName string) (*proxmoxv1alpha1.QEMUStatus, error) {
	var VirtualMachineIP string
	var VirtualMachineOS string
	var VirtualmachineStatus *proxmoxv1alpha1.QEMUStatus
	// Get VM status
	// Check if VM is already created
	if CheckVM(vmName, nodeName) {
		// Get VMID
		VirtualMachine, err := getVirtualMachine(vmName, nodeName)
		if err != nil {
			panic(err)
		}
		if AgentIsRunning(vmName, nodeName) {
			VirtualMachineIP = GetVMIPAddress(vmName, nodeName)
			VirtualMachineOS = GetOSInfo(vmName, nodeName)
		} else {
			VirtualMachineIP = "nil"
			VirtualMachineOS = "nil"
		}
		VirtualmachineStatus = &proxmoxv1alpha1.QEMUStatus{
			State:     VirtualMachine.Status,
			ID:        int(VirtualMachine.VMID),
			Node:      VirtualMachine.Node,
			Uptime:    GetVMUptime(vmName, nodeName),
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

func UpdateVM(vm *proxmoxv1alpha1.VirtualMachine) bool {
	vmName := vm.Spec.Name
	nodeName := vm.Spec.NodeName
	updateStatus := false
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Update VM
	var cpuOption proxmox.VirtualMachineOption
	var memoryOption proxmox.VirtualMachineOption
	cpuOption.Name = virtualMachineCPUOption
	memoryOption.Name = virtualMachineMemoryOption
	switch CheckVMType(vm) {
	case virtualMachineTemplateType:
		cpuOption.Value = vm.Spec.Template.Cores
		memoryOption.Value = uint64(vm.Spec.Template.Memory)
		metrics.SetVirtualMachineCPUCores(vmName, vm.Namespace, float64(vm.Spec.Template.Cores))
		metrics.SetVirtualMachineMemory(vmName, vm.Namespace, float64(vm.Spec.Template.Memory))
	case virtualMachineScratchType:
		cpuOption.Value = vm.Spec.VMSpec.Cores
		memoryOption.Value = uint64(vm.Spec.VMSpec.Memory)
		metrics.SetVirtualMachineCPUCores(vmName, vm.Namespace, float64(vm.Spec.VMSpec.Cores))
		metrics.SetVirtualMachineMemory(vmName, vm.Namespace, float64(vm.Spec.VMSpec.Memory))
	default:
		log.Log.Info(fmt.Sprintf("VM %s doesn't have any template or vmSpec defined", vmName))
	}
	VirtualMachineMem := VirtualMachine.MaxMem / 1024 / 1024 // As MB

	if VirtualMachine.CPUs != cpuOption.Value || VirtualMachineMem != memoryOption.Value {
		var task *proxmox.Task
		task, err = VirtualMachine.Config(ctx, cpuOption, memoryOption)
		if err != nil {
			panic(err)
		}

		_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
		switch taskCompleted {
		case false:
			log.Log.Error(taskErr, "Can't update VM")
		case true:
			log.Log.Info(fmt.Sprintf("VM %s updating", vmName))
		default:
			log.Log.Info("VM is already updated")
		}
		// After config update, restart VM
		task = RestartVM(vmName, nodeName)
		_, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx, virtualMachineRestartTimesNum, virtualMachineRestartSteps)
		if !taskCompleted {
			log.Log.Error(taskErr, "Can't restart VM")
		} else {
			updateStatus = true
		}
	}
	return updateStatus
}

func CreateManagedVM(managedVM string) *proxmoxv1alpha1.ManagedVirtualMachine {
	nodeName := GetNodeOfVM(managedVM)
	cores, memory, disk := GetManagedVMSpec(managedVM, nodeName)

	// IF POD_NAMESPACE is not set, set it to default
	if os.Getenv("POD_NAMESPACE") == "" {
		os.Setenv("POD_NAMESPACE", "default")
	}

	// Create VM object
	VirtualMachine := &proxmoxv1alpha1.ManagedVirtualMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "proxmox.alperen.cloud/v1alpha1",
			Kind:       "ManagedVirtualMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ToLower(managedVM),
			Namespace: os.Getenv("POD_NAMESPACE"),
		},
		Spec: proxmoxv1alpha1.ManagedVirtualMachineSpec{
			Name:     managedVM,
			NodeName: nodeName,
			Cores:    cores,
			Memory:   memory,
			Disk:     disk,
		},
	}
	return VirtualMachine
}

func GetManagedVMs() []string {
	// Get VMs with tag managedVirtualMachineTag
	nodes := GetOnlineNodes()
	var ManagedVMs []string
	for _, node := range nodes {
		node, err := Client.Node(ctx, node)
		if err != nil {
			panic(err)
		}
		VirtualMachines, err := node.VirtualMachines(ctx)
		if err != nil {
			panic(err)
		}
		for _, VirtualMachine := range VirtualMachines {
			vmTags := strings.Split(VirtualMachine.Tags, ";")
			// Check if VM has managedVirtualMachineTag but not kubemox tag
			if utils.StringInSlice(ManagedVirtualMachineTag, vmTags) && !utils.StringInSlice(virtualMachineTag, vmTags) {
				ManagedVMs = append(ManagedVMs, VirtualMachine.Name)
			}
		}
	}
	return ManagedVMs
}

func UpdateManagedVM(ctx context.Context, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) {
	managedVMName := managedVM.Spec.Name
	nodeName := GetNodeOfVM(managedVMName)
	logger := log.FromContext(ctx)
	if GetVMState(managedVMName, nodeName) != VirtualMachineRunningState {
		// Break if VM is not running
		logger.Info(fmt.Sprintf("Managed virtual machine %s is not running, update can't be applied", managedVMName))
		return
	} else {
		VirtualMachine, err := getVirtualMachine(managedVMName, nodeName)
		if err != nil {
			logger.Error(err, "Error getting VM for managed VM update")
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
		// Add metrics
		metrics.SetManagedVirtualMachineCPUCores(managedVMName, managedVM.Namespace, float64(managedVM.Spec.Cores))
		metrics.SetManagedVirtualMachineMemory(managedVMName, managedVM.Namespace, float64(managedVM.Spec.Memory))

		if VirtualMachine.CPUs != managedVM.Spec.Cores || VirtualMachineMem != uint64(managedVM.Spec.Memory) {
			// Update VM
			// log.Log.Info(fmt.Sprintf("The comparison between CR and external resource: CPU: %d, %d
			// || Memory: %d, %d", managedVM.Spec.Cores, VirtualMachine.CPUs, managedVM.Spec.Memory, VirtualMachineMem))
			task, err := VirtualMachine.Config(ctx, cpuOption, memoryOption)
			if err != nil {
				panic(err)
			}
			_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
			switch taskCompleted {
			case false:
				logger.Error(taskErr, "Can't update managed VM")
			case true:
				logger.Info(fmt.Sprintf("Managed VM %s has been updated", managedVMName))
			default:
				logger.Info("Managed VM is already updated")
			}
			task = RestartVM(managedVMName, nodeName)
			_, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx, virtualMachineRestartTimesNum, virtualMachineRestartSteps)
			if !taskCompleted {
				logger.Error(taskErr, "Can't restart managed VM")
			}
		}
	}
}

func CreateVMSnapshot(vmName, snapshotName string) (statusCode int) {
	nodeName := GetNodeOfVM(vmName)
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for snapshot creation")
	}
	// Create snapshot
	task, err := VirtualMachine.NewSnapshot(ctx, snapshotName)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 10)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't create snapshot for the VirtualMachine %s", vmName)
		return 1
	case true:
		return 0
	default:
		log.Log.Info("VirtualMachine has already a snapshot with the same name")
		return 2
	}
}

func GetVMSnapshots(vmName string) ([]string, error) {
	nodeName := GetNodeOfVM(vmName)
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for snapshot listing")
	}
	// Get snapshots
	snapshots, err := VirtualMachine.Snapshots(ctx)
	if err != nil {
		log.Log.Error(err, "Error getting snapshots")
	}
	var snapshotNames []string
	for _, snapshot := range snapshots {
		snapshotNames = append(snapshotNames, snapshot.Name)
	}
	return snapshotNames, err
}

func VMSnapshotExists(vmName, snapshotName string) bool {
	snapshots, err := GetVMSnapshots(vmName)
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

func RemoveVirtualMachineTag(vmName, nodeName, tag string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for removing tag")
	}
	removeTagTask, err := VirtualMachine.RemoveTag(ctx, tag)
	_, taskCompleted, taskErr := removeTagTask.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Error removing tag from VirtualMachine")
	}
	return err
}

func GetNetworkConfiguration(vmName, nodeName string) (map[string]string, error) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		return make(map[string]string), err
	}
	// Get all networks of VM
	return VirtualMachine.VirtualMachineConfig.MergeNets(), nil
}

func parseNetworkConfiguration(networks map[string]string) ([]proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork, error) {
	var networkConfiguration []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork

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
		networkConfiguration = append(networkConfiguration, proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork{
			Model:  networkModel,
			Bridge: networkBridge,
		})
	}
	return networkConfiguration, nil
}

func ConfigureVirtualMachine(vm *proxmoxv1alpha1.VirtualMachine) error {
	err := configureVirtualMachineNetwork(vm)
	if err != nil {
		return err
	}
	err = configureVirtualMachineDisk(vm)
	if err != nil {
		return err
	}

	err = configureVirtualMachinePCI(vm)
	if err != nil {
		return err
	}
	return nil
}

func deleteVirtualMachineOption(vm *proxmoxv1alpha1.VirtualMachine, option string) (proxmox.Task, error) {
	nodeName := vm.Spec.NodeName
	virtualMachine, err := getVirtualMachine(vm.Name, nodeName)
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

func updateNetworkConfig(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine, i int, networks []proxmoxv1alpha1.VirtualMachineSpecTemplateNetwork) error {
	// Get the network model&bridge name
	networkModel := networks[i].Model
	networkBridge := networks[i].Bridge
	// Update the network configuration
	virtualMachine, err := getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating network configuration")
	}
	_, err = virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  netStr + strconv.Itoa(i),
		Value: networkModel + "," + "bridge=" + networkBridge,
	})
	return err
}

func configureVirtualMachineNetwork(vm *proxmoxv1alpha1.VirtualMachine) error {
	networks := vm.Spec.Template.Network
	// Get actual network configuration
	virtualMachineNetworks, err := GetNetworkConfiguration(vm.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	virtualMachineNetworksParsed, err := parseNetworkConfiguration(virtualMachineNetworks)
	if err != nil {
		return err
	}
	// Check if network configuration is different
	if !reflect.DeepEqual(networks, virtualMachineNetworksParsed) {
		// The desired network configuration is different than the actual one
		log.Log.Info(fmt.Sprintf("Updating network configuration for VirtualMachine %s", vm.Name))
		// Update the network configuration
		for i := len(networks); i < len(virtualMachineNetworksParsed); i++ {
			// Remove the network configuration
			log.Log.Info(fmt.Sprintf("Removing the network configuration for net%d of VM %s", i, vm.Spec.Name))
			taskID, _ := deleteVirtualMachineOption(vm, "net"+strconv.Itoa(i))
			_, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 3)
			if !taskCompleted {
				log.Log.Error(taskErr, "Error removing network configuration from VirtualMachine")
			}
		}
		for i := len(virtualMachineNetworksParsed); i < len(networks); i++ {
			// Add the network configuration
			log.Log.Info(fmt.Sprintf("Adding the network configuration for net%d of VM %s", i, vm.Spec.Name))
			err = updateNetworkConfig(ctx, vm, i, networks)
			if err != nil {
				return err
			}
		}
		for i := 0; i < len(virtualMachineNetworksParsed); i++ {
			// Check if the network configuration is different
			if len(networks) != 0 && !reflect.DeepEqual(networks[i], virtualMachineNetworksParsed[i]) {
				// Update the network configuration
				log.Log.Info(fmt.Sprintf("Updating the network configuration for net%d of VM %s", i, vm.Spec.Name))
				// Get the network model&bridge name
				err = updateNetworkConfig(ctx, vm, i, networks)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func GetDiskConfiguration(vm *proxmoxv1alpha1.VirtualMachine) (map[string]string, error) {
	nodeName := vm.Spec.NodeName
	VirtualMachine, err := getVirtualMachine(vm.Name, nodeName)
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

func configureVirtualMachineDisk(vm *proxmoxv1alpha1.VirtualMachine) error {
	// Get VM disk spec and actual disk configuration
	disks := vm.Spec.Template.Disk
	virtualMachineDisks, err := GetDiskConfiguration(vm)
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
	if err := applyDiskChanges(ctx, vm, disksToAdd, disksToUpdate, disksToDelete); err != nil {
		return err
	}
	return nil
}

func classifyDisks(desiredDisks, actualDisks []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) (
	disksToAdd, disksToUpdate, disksToDelete []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) {
	getKey := func(disk proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) string {
		return disk.Device
	}
	return classifyItems(desiredDisks, actualDisks, getKey)
}

func classifyPCIs(desiredPcis, actualPcis []proxmoxv1alpha1.PciDevice) (
	pcisToAdd, pcisToUpdate, pcisToDelete []proxmoxv1alpha1.PciDevice) {
	getKey := func(pci proxmoxv1alpha1.PciDevice) string {
		return pci.DeviceID
	}

	return classifyItems(desiredPcis, actualPcis, getKey)
}

func applyDiskChanges(
	ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine,
	disksToAdd, disksToUpdate, disksToDelete []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) error {
	getDeviceID := func(disk proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) string {
		return disk.Device
	}

	return applyChanges(
		ctx,
		vm,
		disksToAdd,
		disksToUpdate,
		disksToDelete,
		getDeviceID,
		addDiskConfig,
		updateDiskConfig,
		"Disk",
	)
}

func parseDiskConfiguration(disks map[string]string) ([]proxmoxv1alpha1.VirtualMachineSpecTemplateDisk, error) {
	var diskConfiguration []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk

	// Parse disks to use as VirtualMachineSpecTemplateDisk
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
				diskSize, err = strconv.Atoi(strings.TrimSuffix(strings.Split(part, "=")[1], "G"))
				if err != nil {
					return nil, err
				}
				break
			}
		}
		diskConfiguration = append(diskConfiguration, proxmoxv1alpha1.VirtualMachineSpecTemplateDisk{
			Storage: diskStorage,
			Size:    diskSize,
			Device:  device,
		})
	}
	return diskConfiguration, nil
}

func updateDiskConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	disk proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) error {
	virtualMachine, err := getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating disk configuration")
	}
	err = virtualMachine.ResizeDisk(ctx, disk.Device, strconv.Itoa(disk.Size)+"G")
	if err != nil {
		log.Log.Error(err, "Error updating disk configuration for VirtualMachine")
	}
	return err
}

func addDiskConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	disk proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) error {
	virtualMachine, err := getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for updating disk configuration")
	}
	taskID, err := virtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  disk.Device,
		Value: disk.Storage + ":" + strconv.Itoa(disk.Size),
	})
	_, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Error updating disk configuration for VirtualMachine")
	}
	return err
}

func CheckVirtualMachineDelta(vm *proxmoxv1alpha1.VirtualMachine) (bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := getVirtualMachine(vm.Name, vm.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
	}
	// Get actual VM's network configuration
	virtualMachineNetworks, err := GetNetworkConfiguration(vm.Name, vm.Spec.NodeName)
	if err != nil {
		return false, err
	}
	VirtualMachineNetworksParsed, err := parseNetworkConfiguration(virtualMachineNetworks)
	if err != nil {
		return false, err
	}
	virtualMachineDisks, err := GetDiskConfiguration(vm)
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
		Cores:    vm.Spec.Template.Cores,
		Sockets:  vm.Spec.Template.Socket,
		Memory:   vm.Spec.Template.Memory,
		Networks: vm.Spec.Template.Network,
		Disks:    sortDisks(vm.Spec.Template.Disk),
	}
	// Compare the actual VM with the desired VM
	if !reflect.DeepEqual(actualVM, desiredVM) {
		return true, nil
	}
	return false, nil
}

func sortDisks(disks []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk) []proxmoxv1alpha1.VirtualMachineSpecTemplateDisk {
	sort.Slice(disks, func(i, j int) bool {
		if disks[i].Storage == disks[j].Storage {
			return disks[i].Device < disks[j].Device
		}
		return disks[i].Storage < disks[j].Storage
	})
	return disks
}

func CheckManagedVMDelta(managedVM *proxmoxv1alpha1.ManagedVirtualMachine) (
	bool, error) {
	// Compare the actual state of the VM with the desired state
	// If there is a difference, return true
	VirtualMachine, err := getVirtualMachine(managedVM.Spec.Name, managedVM.Spec.NodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for watching")
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

func getVirtualMachine(vmName, nodeName string) (*proxmox.VirtualMachine, error) {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return nil, err
	}
	return VirtualMachine, nil
}

func configureVirtualMachinePCI(vm *proxmoxv1alpha1.VirtualMachine) error {
	// Get VM PCI spec and actual PCI configuration
	PCIs := vm.Spec.Template.PciDevices
	virtualMachinePCIs, err := GetPCIConfiguration(vm.Name, vm.Spec.NodeName)
	if err != nil {
		return err
	}
	virtualMachinePCIsParsed, err := parsePCIConfiguration(virtualMachinePCIs)
	if err != nil {
		return err
	}

	// Classify PCI configurations
	PcisToadd, PCIsToUpdate, PcisToDelete := classifyPCIs(PCIs, virtualMachinePCIsParsed)

	// Apply PCI changes
	if err := applyPCIChanges(ctx, vm, PcisToadd, PCIsToUpdate, PcisToDelete); err != nil {
		return err
	}
	return nil
}

func GetPCIConfiguration(vmName, nodeName string) (map[string]string, error) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		return make(map[string]string), err
	}
	PCIs := VirtualMachine.VirtualMachineConfig.MergeHostPCIs()
	return PCIs, nil
}

func parsePCIConfiguration(pcis map[string]string) ([]proxmoxv1alpha1.PciDevice, error) {
	var PCIConfigurations []proxmoxv1alpha1.PciDevice
	var PCIConfiguration proxmoxv1alpha1.PciDevice
	// Parse PCI devices to use as PCI devices
	for i, pci := range pcis {
		pciSplit := strings.Split(pci, ",")
		PCIConfiguration.DeviceID = pciSplit[0]
		for _, pciSplit := range pciSplit[1:] {
			kv := strings.SplitN(pciSplit, "=", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("invalid format for PCI configuration: %s", pciSplit)
			}
			key := kv[0]

			// Check for the PCIE and x-vga keys
			switch key {
			case "pcie":
				PCIConfiguration.PCIE = true
			case "x-vga":
				PCIConfiguration.PrimaryGPU = true
			}
		}
		index, _ := strconv.Atoi(i)
		PCIConfigurations[index] = PCIConfiguration
	}
	return PCIConfigurations, nil
}

func applyPCIChanges(
	ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine,
	pcisToAdd, pcisToUpdate, pcisToDelete []proxmoxv1alpha1.PciDevice,
) error {
	getDeviceID := func(pci proxmoxv1alpha1.PciDevice) string {
		return pci.DeviceID
	}

	return applyChanges(
		ctx,
		vm,
		pcisToAdd,
		pcisToUpdate,
		pcisToDelete,
		getDeviceID,
		updatePCIConfig,
		updatePCIConfig,
		"PCI device",
	)
}

func updatePCIConfig(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	pci proxmoxv1alpha1.PciDevice) error {
	vmName, nodeName := vm.Name, vm.Spec.NodeName

	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	index, err := getIndexOfPCIConfig(vmName, nodeName, pci)
	if err != nil {
		log.Log.Error(err, "Error getting index of PCI configuration")
	}
	// If the type is "mapped" then the value should be "mapped=deviceID"
	var pciID string
	if pci.Type == "mapped" {
		pciID = "mapped=" + pci.DeviceID
	} else {
		pciID = pci.DeviceID
	}

	taskID, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name: "hostpci" + index,
		Value: pciID + func() string {
			var value string
			value += ","
			var pcieSet, xVgaSet bool
			if pci.PCIE {
				value += "pcie=1"
				pcieSet = true
			}
			if pci.PrimaryGPU {
				if pcieSet {
					value += ","
				}
				value += "x-vga=1"
				xVgaSet = true
			}
			if !pcieSet && !xVgaSet {
				return ""
			}

			return value
		}(),
	})
	if err != nil {
		log.Log.Error(err, "Error updating PCI configuration for VirtualMachine")
	}
	_, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Error updating PCI configuration for VirtualMachine")
	}
	// Rebooting VM spawns two different tasks, one for stopping and one for starting and unfortunately you can't track the start so
	// here we should do stop and start separately
	// Stop VM
	err = StopVM(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error stopping VirtualMachine")
	}
	// TODO: Implement something more logical
	// Start VM
	virtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// start the VM
	task, _ := virtualMachine.Start(ctx)
	_, _, err = task.WaitForCompleteStatus(ctx, 5, 3)
	// Check the task status and return the error if it's not completed
	if task.IsFailed {
		log.Log.Error(err, fmt.Sprintf("Error starting VirtualMachine %s while attaching %s PCI device", vmName, pci.DeviceID))
		// Try to revert the changes
		err = revertVirtualMachineOption(vmName, nodeName, "hostpci"+index)
		if err != nil {
			log.Log.Error(err, "Error reverting changes")
		}
		// TODO:
		// Remove the PCI device from the VirtualMachine object YAML
	}
	return err
}

func getIndexOfPCIConfig(vmName, nodeName string, pciDevice proxmoxv1alpha1.PciDevice) (string, error) {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	hostPCIs := VirtualMachine.VirtualMachineConfig.MergeHostPCIs()

	for i, hostPCI := range hostPCIs {
		if strings.Split(hostPCI, ",")[0] == pciDevice.DeviceID {
			return i, nil
		}
	}
	// If device ID is not found, return the 0th index to create it
	return "0", err
}

func revertVirtualMachineOption(vmName, nodeName, value string) error {
	VirtualMachine, err := getVirtualMachine(vmName, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting VM for reverting")
	}
	revertTask, err := VirtualMachine.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "revert",
		Value: value,
	})
	if err != nil {
		log.Log.Error(err, "Error reverting VirtualMachine")
	}
	_, taskCompleted, taskErr := revertTask.WaitForCompleteStatus(ctx, 5, 3)
	if !taskCompleted {
		log.Log.Error(taskErr, "Error occurred while reverting VirtualMachine")
	}
	return taskErr
}
