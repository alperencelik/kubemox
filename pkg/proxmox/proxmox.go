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

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	kubernetes "github.com/alperencelik/kubemox/pkg/kubernetes"
	proxmox "github.com/luthermonson/go-proxmox"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	// Create Proxmox client
	Client         = CreateProxmoxClient()
	ProxmoxIP      = os.Getenv("PROXMOX_IP")
	ProxmoxURL     = fmt.Sprintf("https://%s:8006/api2/json", ProxmoxIP)
	ProxmoxTokenID = os.Getenv("PROXMOX_TOKEN_ID")
	ProxmoxSecret  = os.Getenv("PROXMOX_SECRET")
	ProxmoxSkipTls = os.Getenv("PROXMOX_SKIP_TLS")
)

const (
	// The tag that will be added to VMs in Proxmox cluster
	virtualMachineTag = "kube-proxmox-operator"
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
	proxmoxIP := os.Getenv("PROXMOX_IP")
	proxmoxURL := fmt.Sprintf("https://%s:8006/api2/json", proxmoxIP)
	// Retrieve proxmox credentials from secret

	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	proxmoxTokenID := os.Getenv("PROXMOX_TOKEN_ID")
	proxmoxSecret := os.Getenv("PROXMOX_SECRET")
	//	tokenID := "root@pam!alp"
	//	secret := "0e409a4b-44b9-49d2-8de0-d50e4e56eaee"
	client := proxmox.NewClient(proxmoxURL,
		proxmox.WithClient(&insecureHTTPClient),
		proxmox.WithAPIToken(proxmoxTokenID, proxmoxSecret),
	)
	return client
}

func GetProxmoxVersion() (*proxmox.Version, error) {
	// Get the version of the Proxmox server
	version, err := Client.Version()
	if err != nil {
		return nil, err
	}
	return version, nil
}

func GetNodes() ([]string, error) {
	// Get all nodes
	nodes, err := Client.Nodes()
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
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	templateVMName := vm.Spec.Template.Name
	templateVMID := GetVMID(templateVMName, nodeName)
	templateVM, err := node.VirtualMachine(templateVMID)
	var CloneOptions proxmox.VirtualMachineCloneOptions
	CloneOptions.Full = 1
	CloneOptions.Name = vm.Name
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Creating VM from template: %s", templateVMName))
	newID, task, err := templateVM.Clone(&CloneOptions)
	log.Log.Info(fmt.Sprintf("New VM %s has been creating with ID: %d", vm.Name, newID))
	// Lock VM creation process
	LockVM(vm.Spec.Name)
	// UPID := task.UPID
	// log.Log.Info(fmt.Sprintf("VM creation task UPID: %s", UPID))
	// TODO: Implement a better way to watch the task
	logChan, err := task.Watch(0)
	if err != nil {
		panic(err)
	}
	for logEntry := range logChan {
		// log.Log.Info(logEntry)
		log.Log.Info(fmt.Sprintf("Virtual Machine %s, creation process: %s", vm.Name, logEntry))
	}

	// 	logChan, err := task.Watch(0)
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// 	var wg sync.WaitGroup
	// 	go func() {
	// 		for logEntry := range logChan {
	// 			log.Log.Info(logEntry)
	// 			wg.Done()
	// 		}
	// 	}()
	// 	wg.Add(500)
	// 	wg.Wait()
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(virtualMachineCreateTimesNum, virtualMachineCreateSteps)
	if taskCompleted == false {
		log.Log.Error(taskErr, "Error creating VM")
	} else if taskCompleted == true {
		log.Log.Info(fmt.Sprintf("VM %s has been created", vm.Name))
		// Unlock VM creation process
		UnlockVM(vm.Spec.Name)
	} else {
		log.Log.Info("VM creation task is still running")
	}
	// Add tag to VM
	VirtualMachine, err := node.VirtualMachine(newID)
	VirtualMachine.AddTag(virtualMachineTag)

	if err != nil {
		panic(err)
	}

}

func GetVMID(vmName, nodeName string) int {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	vmList, err := node.VirtualMachines()
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
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	vmList, err := node.VirtualMachines()
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
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Get VM IP
	VirtualMachineIfaces, err := VirtualMachine.AgentGetNetworkIFaces()
	for _, iface := range VirtualMachineIfaces {
		for _, ip := range iface.IPAddresses {
			return ip.IPAddress
		}
	}
	return ""
}

func GetOSInfo(vmName, nodeName string) string {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Get VM OS
	VirtualMachineOS, err := VirtualMachine.AgentOsInfo()
	return VirtualMachineOS.PrettyName
}

func GetVMUptime(vmName, nodeName string) string {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Get VM Uptime as seconds
	VirtualMachineUptime := int(VirtualMachine.Uptime)
	// Convert seconds to format like 1d 2h 3m 4s
	days := VirtualMachineUptime / 86400
	hours := (VirtualMachineUptime - days*86400) / 3600
	minutes := (VirtualMachineUptime - days*86400 - hours*3600) / 60
	seconds := VirtualMachineUptime - days*86400 - hours*3600 - minutes*60
	uptime := fmt.Sprintf("%dd%dh%dm%ds", days, hours, minutes, seconds)
	return uptime
}

func DeleteVM(vmName, nodeName string) *proxmox.Task {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Stop VM
	vmStatus := VirtualMachine.Status
	if vmStatus == "running" {
		task, err := VirtualMachine.Stop()
		if err != nil {
			panic(err)
		}
		_, taskCompleted, taskErr := task.WaitForCompleteStatus(virtualMachineStopTimesNum, virtualMachineStopSteps)

		if taskCompleted == false {
			log.Log.Error(taskErr, "Can't stop VM")
		} else if taskCompleted == true {

			log.Log.Info(fmt.Sprintf("VM %s has been stopped", vmName))
		} else {
			log.Log.Info("VM is already stopped")
		}
	}
	// Delete VM
	task, err := VirtualMachine.Delete()
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(10, 20)
	if taskCompleted == false {
		log.Log.Error(taskErr, "Can't delete VM")
	} else if taskCompleted == true {
		log.Log.Info(fmt.Sprintf("VM %s has been deleted", vmName))
	} else {
		log.Log.Info("VM is already deleted")
	}

	if err != nil {
		panic(err)
	}
	return task
}

func StartVM(vmName, nodeName string) {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Start VM
	task, err := VirtualMachine.Start()
	if err != nil {
		panic(err)
	}
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(virtualMachineStartTimesNum, virtualMachineStartSteps)
	if taskCompleted == false {
		log.Log.Error(taskErr, "Can't start VM")
	} else if taskCompleted == true {
		log.Log.Info(fmt.Sprintf("VM %s has been started", vmName))
	} else {
		log.Log.Info("VM is already started")
	}
}

func RestartVM(vmName, nodeName string) *proxmox.Task {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Restart VM
	task, err := VirtualMachine.Reboot()
	if err != nil {
		panic(err)
	}
	return task
}

func GetVMState(vmName string, nodeName string) string {
	// Gets the VMstate from Proxmox API
	node, err := Client.Node(nodeName)
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	VirtualMachineState := VirtualMachine.Status
	if err != nil {
		panic(err)
	}
	if VirtualMachineState == "running" {
		return "running"
	} else if VirtualMachineState == "stopped" {
		return "stopped"
	} else {
		return "unknown"
	}
}

func AgentIsRunning(vmName, nodeName string) bool {
	// Checks if qemu-agent works on specified VM
	node, _ := Client.Node(nodeName)
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, _ := node.VirtualMachine(vmID)
	err := VirtualMachine.WaitForAgent(AgentTimeoutSeconds)
	if err != nil {
		return false
	} else {
		return true
	}
}

func CreateVMFromScratch(vm *proxmoxv1alpha1.VirtualMachine) {

	nodeName := vm.Spec.NodeName
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	cores := vm.Spec.VmSpec.Cores
	memory := vm.Spec.VmSpec.Memory
	diskName := vm.Spec.VmSpec.Disk.Name
	diskSize := vm.Spec.VmSpec.Disk.Value
	networkName := vm.Spec.VmSpec.Network.Name
	networkValue := vm.Spec.VmSpec.Network.Value
	osName := vm.Spec.VmSpec.OSImage.Name
	osValue := vm.Spec.VmSpec.OSImage.Value

	// Create VM from scratch
	VMOptions := []proxmox.VirtualMachineOption{
		proxmox.VirtualMachineOption{
			Name:  "cores",
			Value: cores,
		},
		proxmox.VirtualMachineOption{
			Name:  "memory",
			Value: memory,
		},
		proxmox.VirtualMachineOption{
			Name:  diskName,
			Value: diskSize,
		},
		proxmox.VirtualMachineOption{
			Name:  networkName,
			Value: networkValue,
		},
		proxmox.VirtualMachineOption{
			Name:  osName,
			Value: osValue,
		},
		proxmox.VirtualMachineOption{
			Name:  "name",
			Value: vm.Spec.Name,
		},
	}
	// Get next VMID
	cluster, err := Client.Cluster()
	if err != nil {
		panic(err)
	}
	vmID, err := cluster.NextID()
	if err != nil {
		panic(err)
	}
	// Create VM
	task, err := node.NewVirtualMachine(vmID, VMOptions...)
	_, taskCompleted, taskErr := task.WaitForCompleteStatus(10, 10)
	if taskCompleted == false {
		log.Log.Error(taskErr, "Can't create VM")
	} else if taskCompleted == true {
		log.Log.Info(fmt.Sprintf("VM %s has been created", vm.Spec.Name))
	} else {
		log.Log.Info("VM is already created")
	}
	VirtualMachine, err := node.VirtualMachine(vmID)
	VirtualMachine.AddTag(virtualMachineTag)
	if err != nil {
		panic(err)
	}

}

func CheckVMType(vm *proxmoxv1alpha1.VirtualMachine) string {
	var VMType string
	if reflect.ValueOf(vm.Spec.Template).IsZero() != true {
		VMType = "template"
	} else if reflect.ValueOf(vm.Spec.VmSpec).IsZero() != true {
		VMType = "scratch"
	} else if reflect.ValueOf(vm.Spec.Template).IsZero() == false && reflect.ValueOf(vm.Spec.VmSpec).IsZero() == false {
		VMType = "faulty"
	} else {
		VMType = "undefined"
	}
	return VMType

}

type VmMutex struct {
	vmName string
	mutex  sync.Mutex
	locked bool
}

var vmMutexes = make(map[string]*VmMutex)

func LockVM(vmName string) {
	vmMutex, ok := vmMutexes[vmName]
	if !ok {
		vmMutex = &VmMutex{
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
		node, err := Client.Node(node)
		if err != nil {
			panic(err)
		}
		VirtualMachines, err := node.VirtualMachines()
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

	nodes, err := Client.Nodes()
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
		node, err := Client.Node(node)
		if err != nil {
			panic(err)
		}
		VirtualMachines, err := node.VirtualMachines()
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

func CheckManagedVMExists(ManagedVM string) bool {
	// Get managed VMs
	ManagedVMs := GetManagedVMs()
	// Check if ManagedVM exists in ManagedVMs
	for _, VM := range ManagedVMs {
		if VM == strings.ToLower(ManagedVM) {
			return true
		}
	}
	return false
}

func GetNodeOfVM(vmName string) string {
	nodes := GetOnlineNodes()
	for _, node := range nodes {
		node, err := Client.Node(node)
		// List VMs on node
		VirtualMachines, err := node.VirtualMachines()
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

func GetManagedVMSpec(ManagedVMName, nodeName string) (int, int, int) {

	// Get spec of VM
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	vmID := GetVMID(ManagedVMName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	cores := VirtualMachine.CPUs
	memory := int(VirtualMachine.MaxMem / 1024 / 1024) // As MB
	disk := int(VirtualMachine.MaxDisk / 1024 / 1024 / 1024)

	return cores, memory, disk
}

func UpdateVMStatus(vmName, nodeName string) (string, int, string, string, string, string, string) {

	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Check if VM is already created
	if CheckVM(vmName, nodeName) == true {
		// Get VMID
		vmID := GetVMID(vmName, nodeName)
		VirtualMachine, err := node.VirtualMachine(vmID)
		if err != nil {
			panic(err)
		}
		// Get VM status
		VirtualMachineState := VirtualMachine.Status
		VirtualMachineID := int(VirtualMachine.VMID)
		VirtualMachineNode := VirtualMachine.Node
		VirtualMachineName := VirtualMachine.Name
		VirtualMachineUptime := GetVMUptime(vmName, nodeName)
		if AgentIsRunning(vmName, nodeName) == true {
			VirtualMachineIP := GetVMIPAddress(vmName, nodeName)
			VirtualMachineOS := GetOSInfo(vmName, nodeName)
			return VirtualMachineState, VirtualMachineID, VirtualMachineUptime, VirtualMachineNode, VirtualMachineName, VirtualMachineIP, VirtualMachineOS
		} else {
			VirtualMachineIP := "nil"
			VirtualMachineOS := "nil"
			return VirtualMachineState, VirtualMachineID, VirtualMachineUptime, VirtualMachineNode, VirtualMachineName, VirtualMachineIP, VirtualMachineOS
		}
	} else {
		return "VM not found", 0, "0", "0", "0", "0", "0"
	}
}

func UpdateVM(vmName, nodeName string, vm *proxmoxv1alpha1.VirtualMachine) {
	node, err := Client.Node(nodeName)
	if err != nil {
		panic(err)
	}
	// Get VMID
	vmID := GetVMID(vmName, nodeName)
	VirtualMachine, err := node.VirtualMachine(vmID)
	// Change hostname
	// Update VM
	var cpuOption proxmox.VirtualMachineOption
	var memoryOption proxmox.VirtualMachineOption
	var Disk, DiskSize string
	var DiskSizeInt int
	cpuOption.Name = "cores"
	memoryOption.Name = "memory"
	if CheckVMType(vm) == "template" {
		cpuOption.Value = vm.Spec.Template.Cores
		memoryOption.Value = uint64(vm.Spec.Template.Memory)
		DiskSize = strconv.Itoa(vm.Spec.Template.Disk[0].Size) + "G"
		Disk = vm.Spec.Template.Disk[0].Type + "0"
		DiskSizeInt = vm.Spec.Template.Disk[0].Size
	} else if CheckVMType(vm) == "scratch" {
		cpuOption.Value = vm.Spec.VmSpec.Cores
		memoryOption.Value = uint64(vm.Spec.VmSpec.Memory)
		// DiskValue := strings.Split(vm.Spec.VmSpec.Disk.Value, ":")[1]
		DiskValue := vm.Spec.VmSpec.Disk.Value
		DiskSize = DiskValue + "G"
		DiskSizeInt, _ = strconv.Atoi(DiskValue)
		Disk = vm.Spec.VmSpec.Disk.Name
	} else {
		log.Log.Info(fmt.Sprintf("VM %s doesn't have any template or vmSpec defined", vmName))
	}
	_, _, _ = Disk, DiskSize, DiskSizeInt

	// Convert disk size to string
	VirtualMachineMaxDisk := VirtualMachine.MaxDisk / 1024 / 1024 / 1024 // As GB
	//// log.Log.Info(fmt.Sprintf("Resizing disk %s to %s", disk, diskSize))
	//// if current disk is lower than the updated disk size then resize the disk else don't do anything
	if VirtualMachineMaxDisk <= uint64(DiskSizeInt) {
		//// Resize Disk
		VirtualMachine.ResizeDisk(Disk, DiskSize)
	} else {
		// log.Log.Info(fmt.Sprintf("VirtualMachineMaxDisk: %d , DiskSizeInt: %d", VirtualMachineMaxDisk, DiskSizeInt))
		// Revert the update on VM object
		// TODO --> This doesn't work for scratch VM's.
		if CheckVMType(vm) == "template" {
			log.Log.Info(fmt.Sprintf("VirtualMachine %s disk %s can't shrink.", vmName, Disk))
			vm.Spec.Template.Disk[0].Size = int(VirtualMachineMaxDisk)
		}
	}

	VirtualMachineMem := VirtualMachine.MaxMem / 1024 / 1024 // As MB

	if VirtualMachine.CPUs != cpuOption.Value || VirtualMachineMem != memoryOption.Value {
		var task *proxmox.Task
		task, err = VirtualMachine.Config(cpuOption, memoryOption)
		if err != nil {
			panic(err)
		}

		_, taskCompleted, taskErr := task.WaitForCompleteStatus(virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
		if taskCompleted == false {
			log.Log.Error(taskErr, "Can't update VM")
		} else if taskCompleted == true {
			log.Log.Info(fmt.Sprintf("VM %s has been updated", vmName))
		} else {
			log.Log.Info("VM is already updated")
		}
		// After config update, restart VM
		task = RestartVM(vmName, nodeName)
		_, taskCompleted, taskErr = task.WaitForCompleteStatus(virtualMachineRestartTimesNum, virtualMachineRestartSteps)
		if taskCompleted == false {
			log.Log.Error(taskErr, "Can't restart VM")
		}

	}
}

func CreateManagedVM(ManagedVM string) *proxmoxv1alpha1.ManagedVirtualMachine {

	nodeName := GetNodeOfVM(ManagedVM)
	cores, memory, disk := GetManagedVMSpec(ManagedVM, nodeName)
	// Create VM object
	VirtualMachine := &proxmoxv1alpha1.ManagedVirtualMachine{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "proxmox.alperen.cloud/v1alpha1",
			Kind:       "ManagedVirtualMachine",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ToLower(ManagedVM),
			Namespace: os.Getenv("POD_NAMESPACE"),
		},
		Spec: proxmoxv1alpha1.ManagedVirtualMachineSpec{
			Name:     ManagedVM,
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

	if GetVMState(managedVMName, nodeName) != "running" {
		// Break if VM is not running
		return
	} else {

		node, err := Client.Node(nodeName)
		if err != nil {
			panic(err)
		}
		// Get VMID
		vmID := GetVMID(managedVMName, nodeName)
		VirtualMachine, err := node.VirtualMachine(vmID)
		VirtualMachineMem := VirtualMachine.MaxMem / 1024 / 1024 // As MB
		var cpuOption proxmox.VirtualMachineOption
		var memoryOption proxmox.VirtualMachineOption
		cpuOption.Name = "cores"
		cpuOption.Value = managedVM.Spec.Cores
		memoryOption.Name = "memory"
		memoryOption.Value = managedVM.Spec.Memory
		// Disk
		diskSize := managedVM.Spec.Disk
		// TODO: Need to retrieve disk name from external resource
		disk := "scsi0"
		VirtualMachineMaxDisk := VirtualMachine.MaxDisk / 1024 / 1024 / 1024 // As GB
		// convert string to uint64
		if VirtualMachineMaxDisk <= uint64(diskSize) {
			// Resize Disk
			VirtualMachine.ResizeDisk(disk, strconv.Itoa(diskSize)+"G")
		} else {
			log.Log.Info(fmt.Sprintf("External resource: %d || Custom Resource: %d", VirtualMachineMaxDisk, diskSize))
			log.Log.Info(fmt.Sprintf("VirtualMachine %s disk %s can't shrink.", managedVMName, disk))
			// Revert the update since it's not possible to shrink disk
			managedVM.Spec.Disk = int(VirtualMachineMaxDisk)
		}
		if VirtualMachine.CPUs != managedVM.Spec.Cores || VirtualMachineMem != uint64(managedVM.Spec.Memory) {
			// Update VM
			// log.Log.Info(fmt.Sprintf("The comparison between CR and external resource: CPU: %d, %d || Memory: %d, %d", managedVM.Spec.Cores, VirtualMachine.CPUs, managedVM.Spec.Memory, VirtualMachineMem))
			task, err := VirtualMachine.Config(cpuOption, memoryOption)
			if err != nil {
				panic(err)
			}
			_, taskCompleted, taskErr := task.WaitForCompleteStatus(virtualMachineUpdateTimesNum, virtualMachineUpdateSteps)
			if taskCompleted == false {
				log.Log.Error(taskErr, "Can't update VM")
			} else if taskCompleted == true {
				log.Log.Info(fmt.Sprintf("VM %s has been updated", managedVMName))
			} else {
				log.Log.Info("VM is already updated")
			}
			task = RestartVM(managedVMName, nodeName)
			_, taskCompleted, taskErr = task.WaitForCompleteStatus(virtualMachineRestartTimesNum, virtualMachineRestartSteps)
			if taskCompleted == false {
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
