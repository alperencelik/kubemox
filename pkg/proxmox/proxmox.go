package proxmox

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	kubernetes "github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/metrics"
	proxmox "github.com/luthermonson/go-proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	// Create Proxmox client
	Client = CreateProxmoxClient()
	ctx    = context.Background()
)

type ProxmoxConfig struct {
	Endpoint              string
	APIEndpoint           string
	InsecureSkipTLSVerify bool
	Username              string
	Password              string
	TokenID               string
	Secret                string
}

var mutex = &sync.Mutex{}

const (
	// The tag that will be added to VMs in Proxmox cluster
	virtualMachineTag          = "kube-proxmox-operator"
	virtualMachineRunningState = "running"
	virtualMachineStoppedState = "stopped"
	virtualMachineTemplateType = "template"
	virtualMachineScratchType  = "scratch"
	virtualMachineCPUOption    = "cores"
	virtualMachineMemoryOption = "memory"
	// The timeout for qemu-agent to start in seconds
	AgentTimeoutSeconds = 10
	// The timeouts for VirtualMachine operations
	// Timeout = operationTimesNum * operationSteps
	virtualMachineCreateTimesNum  = 20
	virtualMachineCreateSteps     = 20
	virtualMachineStartTimesNum   = 10
	virtualMachineStartSteps      = 20
	virtualMachineStopTimesNum    = 10
	virtualMachineStopSteps       = 20
	virtualMachineRestartTimesNum = 10
	virtualMachineRestartSteps    = 20
	virtualMachineUpdateTimesNum  = 2
	virtualMachineUpdateSteps     = 5
	VirtualMachineDeleteTimesNum  = 10
	VirtualMachineDeleteSteps     = 20
)

func CreateProxmoxClient() *proxmox.Client {
	// Create a new client
	endpoint := os.Getenv("PROXMOX_ENDPOINT")
	ProxmoxConfig := &ProxmoxConfig{
		Endpoint:    os.Getenv("PROXMOX_ENDPOINT"),
		APIEndpoint: fmt.Sprintf("https://%s:8006/api2/json", endpoint),
		Username:    os.Getenv("PROXMOX_USERNAME"),
		Password:    os.Getenv("PROXMOX_PASSWORD"),
		TokenID:     os.Getenv("PROXMOX_TOKEN_ID"),
		Secret:      os.Getenv("PROXMOX_SECRET"),
	}

	var httpClient *http.Client
	if os.Getenv("PROXMOX_INSECURE_SKIP_TLS_VERIFY") == "true" {
		ProxmoxConfig.InsecureSkipTLSVerify = true
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, //nolint:gosec // Skipping linting for InsecureSkipVerify due to user choice
				},
			},
		}
	}

	var client *proxmox.Client
	switch {
	case ProxmoxConfig.Username != "" && ProxmoxConfig.Password != "":
		client = proxmox.NewClient(ProxmoxConfig.APIEndpoint,
			proxmox.WithCredentials(&proxmox.Credentials{
				Username: ProxmoxConfig.Username,
				Password: ProxmoxConfig.Password,
			}),
			proxmox.WithHTTPClient(httpClient),
		)
	case ProxmoxConfig.TokenID != "" && ProxmoxConfig.Secret != "":
		client = proxmox.NewClient(ProxmoxConfig.APIEndpoint,
			proxmox.WithAPIToken(ProxmoxConfig.TokenID, ProxmoxConfig.Secret),
			proxmox.WithHTTPClient(httpClient),
		)
	default:
		panic("Proxmox credentials are not defined")
	}
	return client
}

func GetProxmoxVersion() (*proxmox.Version, error) {
	// Get the version of the Proxmox server
	version, err := Client.Version(ctx)
	if err != nil {
		return nil, err
	}
	return version, nil
}

func GetNodes() ([]string, error) {
	// Get all nodes
	nodes, err := Client.Nodes(ctx)
	nodeNames := []string{}
	for _, node := range nodes {
		nodeNames = append(nodeNames, node.Node)
	}
	if err != nil {
		return nil, err
	}
	return nodeNames, nil
}

func CreateVMFromTemplate(vm *proxmoxv1alpha1.VirtualMachine) {
	nodeName := vm.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	templateVMName := vm.Spec.Template.Name
	templateVMID := GetVMID(templateVMName, nodeName)
	templateVM, err := node.VirtualMachine(ctx, templateVMID)
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
		// Unlock VM creation process
		// UnlockVM(vm.Spec.Name)
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
		//	if vm.Name == vmName {
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
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
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
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
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
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Get VM Uptime as seconds
	VirtualMachineUptime := int(VirtualMachine.Uptime)
	// Convert seconds to format like 1d 2h 3m 4s
	uptime := FormatUptime(VirtualMachineUptime)
	return uptime
}

func DeleteVM(vmName, nodeName string) {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	mutex.Lock()
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	mutex.Unlock()
	// Stop VM
	vmStatus := VirtualMachine.Status
	if vmStatus == virtualMachineRunningState {
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
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 3, 20)
	switch {
	case !taskCompleted:
		log.Log.Error(taskErr, "Can't delete VM")
	case taskCompleted:
		log.Log.Info(fmt.Sprintf("VM %s has been deleted", vmName))
	default:
		log.Log.Info("VM is already deleted")
	}
}

func StartVM(vmName, nodeName string) {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Start VM
	task, err := VirtualMachine.Start(ctx)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, virtualMachineStartTimesNum, virtualMachineStartSteps)
	switch {
	case !taskCompleted:
		log.Log.Error(taskErr, "Can't start VM")
	case taskCompleted:
		log.Log.Info(fmt.Sprintf("VM %s has been started", vmName))
	default:
		log.Log.Info("VM is already started")
	}
}

func RestartVM(vmName, nodeName string) *proxmox.Task {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Restart VM
	task, err := VirtualMachine.Reboot(ctx)
	if err != nil {
		panic(err)
	}
	return task
}

func GetVMState(vmName, nodeName string) string {
	// Gets the VMstate from Proxmox API
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		log.Log.Error(err, "Error getting node")
	}
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	VirtualMachineState := VirtualMachine.Status
	if err != nil {
		panic(err)
	}
	switch VirtualMachineState {
	case virtualMachineRunningState:
		return virtualMachineRunningState
	case virtualMachineStoppedState:
		return virtualMachineStoppedState
	default:
		return "unknown"
	}
}

func AgentIsRunning(vmName, nodeName string) bool {
	// Checks if qemu-agent works on specified VM
	node, _ := Client.Node(ctx, nodeName)
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, _ := node.VirtualMachine(ctx, vmID)
	err := VirtualMachine.WaitForAgent(ctx, AgentTimeoutSeconds)
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
			Value: osValue,
		},
		{
			Name:  "name",
			Value: vm.Spec.Name,
		},
	}
	// Get next VMID
	cluster, err := Client.Cluster(ctx)
	if err != nil {
		panic(err)
	}
	vmID, err := cluster.NextID(ctx)
	if err != nil {
		panic(err)
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

type VMMutex struct {
	vmName string
	mutex  sync.Mutex
	locked bool
}

var vmMutexes = make(map[string]*VMMutex)

func LockVM(vmName string) {
	vmMutex, ok := vmMutexes[vmName]
	if !ok {
		vmMutex = &VMMutex{
			vmName: vmName,
		}
		vmMutexes[vmName] = vmMutex
	}
	vmMutex.mutex.Lock()
	vmMutex.locked = true
}

func UnlockVM(vmName string) {
	vmMutex, ok := vmMutexes[vmName]
	if !ok {
		return
	}
	vmMutex.mutex.Unlock()
	vmMutex.locked = false
}

func IsVMLocked(vmName string) bool {
	vmMutex, ok := vmMutexes[vmName]
	if !ok {
		return false
	}
	return vmMutex.locked
}

func GetProxmoxVMs() []string {
	var VMs []string
	nodes := GetOnlineNodes()
	for _, node := range nodes {
		node, err := Client.Node(ctx, node)
		if err != nil {
			panic(err)
		}
		VirtualMachines, err := node.VirtualMachines(ctx)
		if err != nil {
			panic(err)
		}
		for _, vm := range VirtualMachines {
			VMs = append(VMs, vm.Name)
		}
	}
	return VMs
}

func GetOnlineNodes() []string {
	nodes, err := Client.Nodes(ctx)
	var OnlineNodes []string
	if err != nil {
		panic(err)
	}
	for _, node := range nodes {
		if node.Status == "online" {
			OnlineNodes = append(OnlineNodes, node.Node)
		}
	}
	return OnlineNodes
}

func GetControllerVMs() []string {
	// From proxmox get VM's that has tag "kube-proxmox-operator"
	nodes := GetOnlineNodes()
	var ControllerVMs []string
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
			vmTags := VirtualMachine.Tags
			if vmTags == "kube-proxmox-operator" {
				ControllerVMs = append(ControllerVMs, VirtualMachine.Name)
			}
		}
	}
	return ControllerVMs
}

func CheckManagedVMExists(managedVM string) bool {
	// Get managed VMs
	managedVMs := GetManagedVMs()
	// Check if ManagedVM exists in ManagedVMs
	for _, VM := range managedVMs {
		if strings.EqualFold(VM, managedVM) {
			return true
		}
	}
	return false
}

func GetNodeOfVM(vmName string) string {
	nodes := GetOnlineNodes()
	for _, node := range nodes {
		node, err := Client.Node(ctx, node)
		if err != nil {
			panic(err)
		}
		// List VMs on node
		VirtualMachines, err := node.VirtualMachines(ctx)
		if err != nil {
			panic(err)
		}
		for _, vm := range VirtualMachines {
			if strings.EqualFold(vm.Name, vmName) {
				return node.Name
			}
		}
		if err != nil {
			panic(err)
		}
	}
	return ""
}

func GetManagedVMSpec(managedVMName, nodeName string) (cores, memory, disk int) {
	// Get spec of VM
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	vmID := GetVMID(managedVMName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	cores = VirtualMachine.CPUs
	memory = int(VirtualMachine.MaxMem / 1024 / 1024) // As MB
	disk = int(VirtualMachine.MaxDisk / 1024 / 1024 / 1024)

	return cores, memory, disk
}

func UpdateVMStatus(vmName, nodeName string) (string, int, string, string, string, string, string) {

	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Check if VM is already created
	if CheckVM(vmName, nodeName) {
		// Get VMID
		vmID := GetVMID(vmName, nodeName)
		VirtualMachine, err := node.VirtualMachine(ctx, vmID)
		if err != nil {
			panic(err)
		}
		// Get VM status
		VirtualMachineState := VirtualMachine.Status
		VirtualMachineID := int(VirtualMachine.VMID)
		VirtualMachineNode := VirtualMachine.Node
		VirtualMachineName := VirtualMachine.Name
		VirtualMachineUptime := GetVMUptime(vmName, nodeName)
		if AgentIsRunning(vmName, nodeName) {
			VirtualMachineIP := GetVMIPAddress(vmName, nodeName)
			VirtualMachineOS := GetOSInfo(vmName, nodeName)
			return VirtualMachineState, VirtualMachineID, VirtualMachineUptime,
				VirtualMachineNode, VirtualMachineName, VirtualMachineIP, VirtualMachineOS
		} else {
			VirtualMachineIP := "nil"
			VirtualMachineOS := "nil"
			return VirtualMachineState, VirtualMachineID, VirtualMachineUptime, VirtualMachineNode,
				VirtualMachineName, VirtualMachineIP, VirtualMachineOS
		}
	} else {
		return "VM not found", 0, "0", "0", "0", "0", "0"
	}
}

func UpdateVM(vmName, nodeName string, vm *proxmoxv1alpha1.VirtualMachine) {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
	}
	// Change hostname
	// Update VM
	var cpuOption proxmox.VirtualMachineOption
	var memoryOption proxmox.VirtualMachineOption
	var Disk, DiskSize string
	var DiskSizeInt int
	cpuOption.Name = virtualMachineCPUOption
	memoryOption.Name = virtualMachineMemoryOption
	switch CheckVMType(vm) {
	case virtualMachineTemplateType:
		cpuOption.Value = vm.Spec.Template.Cores
		memoryOption.Value = uint64(vm.Spec.Template.Memory)
		DiskSize = strconv.Itoa(vm.Spec.Template.Disk[0].Size) + "G"
		Disk = vm.Spec.Template.Disk[0].Type + "0"
		DiskSizeInt = vm.Spec.Template.Disk[0].Size
		metrics.SetVirtualMachineCPUCores(vmName, vm.Namespace, float64(vm.Spec.Template.Cores))
		metrics.SetVirtualMachineMemory(vmName, vm.Namespace, float64(vm.Spec.Template.Memory))
	case virtualMachineScratchType:
		cpuOption.Value = vm.Spec.VMSpec.Cores
		memoryOption.Value = uint64(vm.Spec.VMSpec.Memory)
		DiskValue := vm.Spec.VMSpec.Disk.Value
		DiskSize = DiskValue + "G"
		DiskSizeInt, _ = strconv.Atoi(DiskValue)
		Disk = vm.Spec.VMSpec.Disk.Name
		metrics.SetVirtualMachineCPUCores(vmName, vm.Namespace, float64(vm.Spec.VMSpec.Cores))
		metrics.SetVirtualMachineMemory(vmName, vm.Namespace, float64(vm.Spec.VMSpec.Memory))
	default:
		log.Log.Info(fmt.Sprintf("VM %s doesn't have any template or vmSpec defined", vmName))
	}

	// Convert disk size to string
	VirtualMachineMaxDisk := VirtualMachine.MaxDisk / 1024 / 1024 / 1024 // As GB
	//// log.Log.Info(fmt.Sprintf("Resizing disk %s to %s", disk, diskSize))
	//// if current disk is lower than the updated disk size then resize the disk else don't do anything
	if VirtualMachineMaxDisk <= uint64(DiskSizeInt) {
		// Resize Disk
		err = VirtualMachine.ResizeDisk(ctx, Disk, DiskSize)
		if err != nil {
			log.Log.Error(err, "Can't resize disk")
		}
	} else if CheckVMType(vm) == virtualMachineTemplateType {
		log.Log.Info(fmt.Sprintf("VirtualMachine %s disk %s can't shrink.", vmName, Disk))
		vm.Spec.Template.Disk[0].Size = int(VirtualMachineMaxDisk)
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
			log.Log.Info(fmt.Sprintf("VM %s has been updated", vmName))
		default:
			log.Log.Info("VM is already updated")
		}
		// After config update, restart VM
		task = RestartVM(vmName, nodeName)
		_, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx, virtualMachineRestartTimesNum, virtualMachineRestartSteps)
		if !taskCompleted {
			log.Log.Error(taskErr, "Can't restart VM")
		}
	}
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

		Status: proxmoxv1alpha1.ManagedVirtualMachineStatus{
			ID: 0,
		},
	}
	return VirtualMachine
}

func GetManagedVMs() []string {
	// Get my custom resource "ManagedVirtualMachine"
	customResource := schema.GroupVersionResource{
		Group:    "proxmox.alperen.cloud",
		Version:  "v1alpha1",
		Resource: "managedvirtualmachines",
	}
	// Get all ManagedVirtualMachines with client-go
	var ManagedVMs []string
	ClientManagedVMs, err := kubernetes.DynamicClient.Resource(customResource).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		panic(err)
	}
	for _, VM := range ClientManagedVMs.Items {
		ManagedVMName := VM.GetName()
		ManagedVMName = strings.ToLower(ManagedVMName)
		ManagedVMs = append(ManagedVMs, ManagedVMName)
	}
	return ManagedVMs
}

func UpdateManagedVM(managedVMName, nodeName string, managedVM *proxmoxv1alpha1.ManagedVirtualMachine) {
	if GetVMState(managedVMName, nodeName) != virtualMachineRunningState {
		// Break if VM is not running
		return
	} else {
		node, err := Client.Node(ctx, nodeName)
		if err != nil {
			panic(err)
		}
		// Get VMID
		vmID := GetVMID(managedVMName, nodeName)
		VirtualMachine, err := node.VirtualMachine(ctx, vmID)
		if err != nil {
			log.Log.Error(err, "Error getting VM")
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
				log.Log.Error(taskErr, "Can't update VM")
			case true:
				log.Log.Info(fmt.Sprintf("VM %s has been updated", managedVMName))
			default:
				log.Log.Info("VM is already updated")
			}
			task = RestartVM(managedVMName, nodeName)
			_, taskCompleted, taskErr = task.WaitForCompleteStatus(ctx, virtualMachineRestartTimesNum, virtualMachineRestartSteps)
			if !taskCompleted {
				log.Log.Error(taskErr, "Can't restart VM")
			}
		}
	}
}

func SubstractSlices(slice1, slice2 []string) []string {
	elements := make(map[string]bool)
	for _, elem := range slice2 {
		elements[elem] = true
	}
	// Create a result slice to store the difference
	var difference []string
	// Iterate through slice1 and check if the element is present in slice2
	for _, elem := range slice1 {
		if !elements[elem] {
			difference = append(difference, elem)
		}
	}
	return difference
}

func SubstractLowercaseSlices(slice1, slice2 []string) []string {
	elementMap := make(map[string]bool)

	for _, elem := range slice2 {
		elementMap[strings.ToLower(elem)] = true
	}
	// Create a result slice to store the difference
	var difference []string
	// Iterate through slice1 and check if the element is present in slice2
	for _, elem := range slice1 {
		if !elementMap[strings.ToLower(elem)] {
			difference = append(difference, elem)
		}
	}
	return difference
}

func CreateVMSnapshot(vmName, snapshotName string) (statusCode int) {
	nodeName := GetNodeOfVM(vmName)
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		log.Log.Error(err, "Error getting VM")
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
		log.Log.Info(fmt.Sprintf("VirtualMachine %s has been snapshotted with %s name", vmName, snapshotName))
		return 0
	default:
		log.Log.Info("VirtualMachine has already a snapshot with the same name")
		return 2
	}
}

func CloneContainer(container *proxmoxv1alpha1.Container) error {
	nodeName := container.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	templateContainerName := container.Spec.Template.Name
	templateContainerID := 101
	templateContainer, err := node.Container(ctx, templateContainerID)
	if err != nil {
		panic(err)
	}

	var CloneOptions proxmox.ContainerCloneOptions
	CloneOptions.Full = 1
	CloneOptions.Hostname = container.Name
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Cloning container %s from template %s", container.Name, templateContainerName))

	_, task, err := templateContainer.Clone(ctx, &CloneOptions)
	if err != nil {
		log.Log.Error(err, "Can't clone container")
	}
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 10)
	if !taskCompleted {
		log.Log.Error(taskErr, "Can't clone container")
	}

	return taskErr
}

func GetContainerID(containerName, nodeName string) int {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		panic(err)
	}
	for _, container := range containers {
		if container.Name == containerName {
			return int(container.VMID)
		}
	}
	return 0
}

func ContainerExists(containerName, nodeName string) bool {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		panic(err)
	}
	for _, container := range containers {
		if container.Name == containerName {
			return true
		}
	}
	return false
}

func GetContainer(containerName, nodeName string) *proxmox.Container {
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	containerID := GetContainerID(containerName, nodeName)
	container, err := node.Container(ctx, containerID)
	if err != nil {
		panic(err)
	}
	return container
}

func StopContainer(containerName, nodeName string) (*proxmox.ContainerStatus, error) {
	// Get container
	log.Log.Info(fmt.Sprintf("Stopping container %s", containerName))
	container := GetContainer(containerName, nodeName)
	// Stop container
	if container.Status == virtualMachineRunningState {
		// Stop container called
		status, err := container.Stop(ctx)
		// Retry method to understand if container is stopped
		for i := 0; i < 5; i++ {
			contStatus := GetContainerState(containerName, nodeName)
			if contStatus == virtualMachineStoppedState {
				break
			} else {
				time.Sleep(5 * time.Second)
			}
		}
		return status, err
	} else {
		return nil, nil
	}
}

func DeleteContainer(containerName, nodeName string) {
	// Get container
	mutex.Lock()
	container := GetContainer(containerName, nodeName)
	mutex.Unlock()
	containerStatus := container.Status
	if containerStatus == virtualMachineRunningState {
		// Stop container
		_, err := StopContainer(containerName, nodeName)
		if err != nil {
			panic(err)
		}
	}
	log.Log.Info(fmt.Sprintf("Deleting container %s", containerName))
	// Delete container
	mutex.Lock()
	// Delete container
	task, err := container.Delete(ctx)
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 5)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't delete container")
	case true:
		log.Log.Info(fmt.Sprintf("Container %s has been deleted", containerName))
	default:
		log.Log.Info("Container is already deleted")
	}
	mutex.Unlock()
}

func StartContainer(containerName, nodeName string) {
	// Get container
	container := GetContainer(containerName, nodeName)
	// Start container
	status, err := container.Start(ctx)
	log.Log.Info(fmt.Sprintf("Container %s status: %s", containerName, status))
	if err != nil {
		log.Log.Error(err, "Can't start container")
	}
}

func GetContainerState(containerName, nodeName string) string {
	// Get container
	container := GetContainer(containerName, nodeName)
	// Get container state
	return container.Status
}

func UpdateContainerStatus(containerName, nodeName string) proxmoxv1alpha1.ContainerStatus {
	var containerStatus proxmoxv1alpha1.ContainerStatus
	container := GetContainer(containerName, nodeName)

	containerStatus.State = container.Status
	containerStatus.ID = int(container.VMID)
	containerStatus.Uptime = FormatUptime(int(container.Uptime))
	containerStatus.Node = container.Node
	containerStatus.Name = container.Name

	return containerStatus
}

func UpdateContainer(container *proxmoxv1alpha1.Container) {
	// Get container from proxmox
	containerName := container.Name
	nodeName := container.Spec.NodeName
	var cpuOption proxmox.ContainerOption
	var memoryOption proxmox.ContainerOption
	cpuOption.Name = virtualMachineCPUOption
	memoryOption.Name = virtualMachineMemoryOption
	ProxmoxContainer := GetContainer(containerName, nodeName)
	// Check if update is needed
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs || container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
		cpuOption.Value = container.Spec.Template.Cores
		memoryOption.Value = container.Spec.Template.Memory
		// Update container
		_, err := ProxmoxContainer.Config(ctx, cpuOption, memoryOption)
		if err != nil {
			panic(err)
		} else {
			log.Log.Info(fmt.Sprintf("Container %s has been updated", containerName))
		}
		// Config of container doesn't require restart
	}
}

func RestartContainer(containerName, nodeName string) bool {
	// Get container
	container := GetContainer(containerName, nodeName)
	// Restart container
	_, err := container.Reboot(ctx)
	if err != nil {
		panic(err)
	}
	// Retry method to understand if container is stopped
	for i := 0; i < 5; i++ {
		contStatus := GetContainerState(containerName, nodeName)
		if contStatus == virtualMachineRunningState {
			return true
		} else {
			time.Sleep(5 * time.Second)
		}
	}
	return false
}

func FormatUptime(uptime int) string {
	// Convert seconds to format like 1d 2h 3m 4s
	days := uptime / 86400
	hours := (uptime - days*86400) / 3600
	minutes := (uptime - days*86400 - hours*3600) / 60
	seconds := uptime - days*86400 - hours*3600 - minutes*60
	uptimeString := fmt.Sprintf("%dd%dh%dm%ds", days, hours, minutes, seconds)
	return uptimeString
}
