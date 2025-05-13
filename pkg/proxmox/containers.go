package proxmox

import (
	"fmt"
	"time"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	proxmox "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CloneContainer clones a container from a template
func (pc *ProxmoxClient) CloneContainer(container *proxmoxv1alpha1.Container) error {
	// Returning an error is quite reasonable here since that error moved up
	// to the controller and will be handled there as requeue
	nodeName := container.Spec.NodeName
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return err
	}
	templateContainerName := container.Spec.Template.Name
	templateContainerID, err := pc.GetContainerID(templateContainerName, nodeName)
	if err != nil {
		return err
	}
	templateContainer, err := node.Container(ctx, templateContainerID)
	if err != nil {
		return err
	}

	var CloneOptions proxmox.ContainerCloneOptions
	CloneOptions.Full = 1
	CloneOptions.Hostname = container.Name
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Cloning container %s from template %s", container.Name, templateContainerName))

	_, task, err := templateContainer.Clone(ctx, &CloneOptions)
	if err != nil {
		log.Log.Error(err, "Can't clone container")
		return err
	}
	taskStatus, taskCompleted, taskWaitErr := task.WaitForCompleteStatus(ctx, 5, 10)
	if !taskStatus {
		// Return the task.ExitStatus as an error
		return &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return taskWaitErr
	}
	return nil
}

func (pc *ProxmoxClient) GetContainerID(containerName, nodeName string) (int, error) {
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return 0, err
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		return 0, err
	}
	for _, container := range containers {
		if container.Name == containerName {
			return int(container.VMID), nil
		}
	}
	return 0, nil
}

func (pc *ProxmoxClient) ContainerExists(containerName, nodeName string) (bool, error) {
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return false, err
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		return false, err
	}
	for _, container := range containers {
		if container.Name == containerName {
			return true, nil
		}
	}
	return false, nil
}

func (pc *ProxmoxClient) GetContainer(containerName, nodeName string) (*proxmox.Container, error) {
	node, err := pc.Client.Node(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	containerID, err := pc.GetContainerID(containerName, nodeName)
	if err != nil {
		return nil, err
	}
	container, err := node.Container(ctx, containerID)
	if err != nil {
		return nil, err
	}
	return container, nil
}

func (pc *ProxmoxClient) StopContainer(containerName, nodeName string) error {
	// Get container
	log.Log.Info(fmt.Sprintf("Stopping container %s", containerName))
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return err
	}
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

func (pc *ProxmoxClient) DeleteContainer(containerName, nodeName string) error {
	// Get container
	mutex.Lock()
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return err
	}
	mutex.Unlock()
	containerStatus := container.Status
	if containerStatus == VirtualMachineRunningState {
		// Stop container
		err = pc.StopContainer(containerName, nodeName)
		if err != nil {
			return err
		}
	}
	log.Log.Info(fmt.Sprintf("Deleting container %s", containerName))
	// Delete container
	mutex.Lock()
	// Delete container
	task, err := container.Delete(ctx)
	if err != nil {
		return err
	}
	// TODO: Change all task.WaitForCompleteStatus stuff
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
	return nil
}

func (pc *ProxmoxClient) StartContainer(containerName, nodeName string) error {
	// Get container
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return err
	}
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

func (pc *ProxmoxClient) GetContainerState(containerName, nodeName string) (string, error) {
	// Get container
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return "", err
	}
	// Get container state
	return container.Status, nil
}

func (pc *ProxmoxClient) UpdateContainerStatus(containerName, nodeName string) (proxmoxv1alpha1.QEMUStatus, error) {
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return proxmoxv1alpha1.QEMUStatus{}, err
	}

	containerStatus := proxmoxv1alpha1.QEMUStatus{
		State:  container.Status,
		Node:   container.Node,
		Uptime: utils.FormatUptime(int(container.Uptime)),
		ID:     int(container.VMID),
	}
	return containerStatus, nil
}

func (pc *ProxmoxClient) UpdateContainer(container *proxmoxv1alpha1.Container) error {
	// Get container from proxmox
	containerName := container.Name
	nodeName := container.Spec.NodeName
	var cpuOption proxmox.ContainerOption
	var memoryOption proxmox.ContainerOption
	cpuOption.Name = virtualMachineCPUOption
	memoryOption.Name = virtualMachineMemoryOption
	ProxmoxContainer, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return err
	}
	// Check if update is needed
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs || container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
		cpuOption.Value = container.Spec.Template.Cores
		memoryOption.Value = container.Spec.Template.Memory
		// Update container
		_, err := ProxmoxContainer.Config(ctx, cpuOption, memoryOption)
		if err != nil {
			return err
		} else {
			log.Log.Info(fmt.Sprintf("Container %s has been updated", containerName))
		}
		// Config of container doesn't require restart
	}
	return nil
}

func (pc *ProxmoxClient) RestartContainer(containerName, nodeName string) (bool, error) {
	// Get container
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return false, err
	}
	// Restart container
	_, err = container.Reboot(ctx)
	if err != nil {
		return false, err
	}
	// Retry method to understand if container is stopped
	for i := 0; i < 5; i++ {
		contStatus, errr := pc.GetContainerState(containerName, nodeName)
		if errr != nil {
			return false, errr
		}
		if contStatus == VirtualMachineRunningState {
			return true, nil
		} else {
			time.Sleep(5 * time.Second)
		}
	}
	return false, err
}

func (pc *ProxmoxClient) CheckContainerDelta(container *proxmoxv1alpha1.Container) (bool, error) {
	// Get container
	containerName := container.Name
	nodeName := container.Spec.NodeName
	ProxmoxContainer, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return false, err
	}
	// Check if update is needed
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs || container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
		return true, nil
	}
	return false, nil
}

func (pc *ProxmoxClient) IsContainerReady(container *proxmoxv1alpha1.Container) (bool, error) {
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	ProxmoxContainer, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return false, err
	}

	if ProxmoxContainer.VMID == 0 {
		return false, nil
	}
	return true, nil
}
