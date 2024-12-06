package proxmox

import (
	"fmt"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	proxmox "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func CloneContainer(container *proxmoxv1alpha1.Container) error {
	nodeName := container.Spec.NodeName
	node, err := Client.Node(ctx, nodeName)
	if err != nil {
		panic(err)
	}
	templateContainerName := container.Spec.Template.Name
	templateContainerID := GetContainerID(templateContainerName, nodeName)
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

func StopContainer(containerName, nodeName string) error {
	// Get container
	log.Log.Info(fmt.Sprintf("Stopping container %s", containerName))
	container := GetContainer(containerName, nodeName)
	// Stop container
	if container.Status == VirtualMachineRunningState {
		// Stop container called
		taskID, err := container.Stop(ctx)
		if err != nil {
			return err
		}
		_, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 3, 5)
		if !taskCompleted {
			return taskErr
		}
	}
	return nil
}

func DeleteContainer(containerName, nodeName string) {
	// Get container
	mutex.Lock()
	container := GetContainer(containerName, nodeName)
	mutex.Unlock()
	containerStatus := container.Status
	if containerStatus == VirtualMachineRunningState {
		// Stop container
		err := StopContainer(containerName, nodeName)
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

func StartContainer(containerName, nodeName string) error {
	// Get container
	container := GetContainer(containerName, nodeName)
	// Start container
	taskID, err := container.Start(ctx)
	if err != nil {
		return err
	}
	_, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 5, 5)
	switch taskCompleted {
	case false:
		log.Log.Error(taskErr, "Can't start container")
	case true:
		log.Log.Info(fmt.Sprintf("Container %s has been started", containerName))
	default:
		log.Log.Info("Container is already started")
	}
	return nil
}

func GetContainerState(containerName, nodeName string) string {
	// Get container
	container := GetContainer(containerName, nodeName)
	// Get container state
	return container.Status
}

func UpdateContainerStatus(containerName, nodeName string) proxmoxv1alpha1.QEMUStatus {
	container := GetContainer(containerName, nodeName)

	containerStatus := proxmoxv1alpha1.QEMUStatus{
		State:  container.Status,
		Node:   container.Node,
		Uptime: utils.FormatUptime(int(container.Uptime)),
		ID:     int(container.VMID),
	}
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
		if contStatus == VirtualMachineRunningState {
			return true
		} else {
			time.Sleep(5 * time.Second)
		}
	}
	return false
}

func CheckContainerDelta(container *proxmoxv1alpha1.Container) (bool, error) {
	// Get container
	containerName := container.Name
	nodeName := container.Spec.NodeName
	ProxmoxContainer := GetContainer(containerName, nodeName)
	// Check if update is needed
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs || container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
		return true, nil
	}
	return false, nil
}

func IsContainerReady(container *proxmoxv1alpha1.Container) (bool, error) {
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	ProxmoxContainer := GetContainer(containerName, nodeName)

	if ProxmoxContainer.VMID == 0 {
		return false, nil
	}
	return true, nil
}
