package proxmox

import (
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"
	"unsafe"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/utils"
	proxmox "github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func isLXCNotRunningErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not running")
}

func setProxmoxContainerClient(c *proxmox.Container, client *proxmox.Client) {
	rv := reflect.ValueOf(c).Elem()
	f := rv.FieldByName("client")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(client))
}

func (pc *ProxmoxClient) lxcFromConfigWhenStopped(nodeName string, containerID int) (*proxmox.Container, error) {
	cc := new(proxmox.ContainerConfig)
	path := fmt.Sprintf("/nodes/%s/lxc/%d/config", nodeName, containerID)
	if err := pc.Client.Get(ctx, path, &cc); err != nil {
		return nil, err
	}
	c := &proxmox.Container{
		Node:            nodeName,
		Status:          VirtualMachineStoppedState,
		ContainerConfig: cc,
		VMID:            proxmox.StringOrUint64(uint64(containerID)),
		Name:            cc.Hostname,
		CPUs:            cc.Cores,
	}
	if cc.Memory > 0 {
		c.MaxMem = uint64(cc.Memory) * 1024 * 1024
	}
	setProxmoxContainerClient(c, pc.Client)
	return c, nil
}

// parseIPv4FromLXCInet parses Proxmox LXC interface Inet (e.g. "192.168.1.5/24" or "192.168.1.5").
func parseIPv4FromLXCInet(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "/") {
		ip, _, err := net.ParseCIDR(raw)
		if err != nil || ip == nil {
			return ""
		}
		if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
			return ip4.String()
		}
		return ""
	}
	ip := net.ParseIP(raw)
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
			return ip4.String()
		}
	}
	return ""
}

func firstIPv4FromContainerInterfaces(ifaces proxmox.ContainerInterfaces) string {
	for _, iface := range ifaces {
		if strings.EqualFold(iface.Name, "lo") {
			continue
		}
		if v := parseIPv4FromLXCInet(iface.Inet); v != "" {
			return v
		}
	}
	return ""
}

func lxcOSInfoFromContainer(c *proxmox.Container) string {
	if c != nil && c.ContainerConfig != nil {
		if t := strings.TrimSpace(c.ContainerConfig.OSType); t != "" {
			return t
		}
	}
	return "LXC"
}

// CloneContainer clones a container from a template
func (pc *ProxmoxClient) CloneContainer(container *proxmoxv1alpha1.Container) error {
	// Returning an error is quite reasonable here since that error moved up
	// to the controller and will be handled there as requeue
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	if _, err := pc.getNode(ctx, nodeName); err != nil {
		return err
	}
	if container.Spec.Template.Image != nil {
		return pc.CreateContainerFromOCIImage(container)
	}
	templateContainerName := TemplateContainerName(container.Spec.Template)
	templateContainerID, err := pc.GetContainerID(templateContainerName, nodeName)
	if err != nil {
		return err
	}
	// If templateContainerID is 0, return error that template not found
	if templateContainerID == 0 {
		return fmt.Errorf("template container %s not found on node %s", templateContainerName, nodeName)
	}
	templateContainer, err := pc.GetContainer(templateContainerName, nodeName)
	if err != nil {
		return err
	}

	var CloneOptions proxmox.ContainerCloneOptions
	CloneOptions.Full = 1
	CloneOptions.Hostname = containerName
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Cloning container %s from template %s", containerName, templateContainerName))

	newID, task, err := templateContainer.Clone(ctx, &CloneOptions)
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
	// Cache the new container ID
	pc.setCachedContainerID(nodeName, containerName, newID)
	return nil
}

func (pc *ProxmoxClient) GetContainerID(containerName, nodeName string) (int, error) {
	// Get node
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return 0, err
	}
	// If it's cached, return it
	if cachedID, ok := pc.getCachedContainerID(nodeName, containerName); ok {
		return cachedID, nil
	}
	// If not cached, fetch from API
	containers, err := node.Containers(ctx)
	if err != nil {
		return 0, err
	}
	for _, container := range containers {
		if container.Name == containerName {
			pc.setCachedContainerID(nodeName, containerName, int(container.VMID))
			return int(container.VMID), nil
		}
	}
	return 0, nil
}

func (pc *ProxmoxClient) ContainerExists(containerName, nodeName string) (bool, error) {
	// Check cache
	if _, ok := pc.getCachedContainerID(nodeName, containerName); ok {
		return true, nil
	}

	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return false, err
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		return false, err
	}
	for _, container := range containers {
		if container.Name == containerName {
			pc.setCachedContainerID(nodeName, containerName, int(container.VMID))
			return true, nil
		}
	}
	return false, nil
}

func (pc *ProxmoxClient) GetContainer(containerName, nodeName string) (*proxmox.Container, error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	containerID, err := pc.GetContainerID(containerName, nodeName)
	if err != nil {
		return nil, err
	}
	if containerID == 0 {
		return nil, fmt.Errorf("container %s not found", containerName)
	}
	// Check cache for container object
	if container := pc.getCachedContainer(nodeName, containerID); container != nil {
		return container, nil
	}
	// If not in cache, fetch from API
	container, err := node.Container(ctx, containerID)
	if err != nil {
		if isLXCNotRunningErr(err) {
			container, err = pc.lxcFromConfigWhenStopped(nodeName, containerID)
		}
		if err != nil {
			return nil, err
		}
	}
	// Store in cache
	pc.setCachedContainer(nodeName, containerID, container)

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
	pc.deleteCachedContainer(nodeName, containerName)
	mutex.Unlock()
	return nil
}

func (pc *ProxmoxClient) StartContainer(containerName, nodeName string) error {
	// Get container
	container, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return err
	}
	// TODO: Main problem here is that if the cache is stale, the status will be stale as well
	// and the operator will try to start an already running VM.
	err = container.Ping(ctx)
	if err != nil {
		if !isLXCNotRunningErr(err) {
			return err
		}
	} else if container.Status == VirtualMachineRunningState {
		return nil
	}
	taskID, err := container.Start(ctx)
	if err != nil {
		return err
	}
	taskStatus, taskCompleted, taskErr := taskID.WaitForCompleteStatus(ctx, 10, 3)
	if !taskCompleted {
		if taskErr != nil {
			log.Log.Error(taskErr, "Can't start container")
			return taskErr
		}
		return fmt.Errorf("timeout waiting for container %s start task to finish", containerName)
	}
	if !taskStatus {
		return &TaskError{ExitStatus: taskID.ExitStatus}
	}
	log.Log.Info(fmt.Sprintf("Container %s has been started", containerName))
	pc.deleteCachedContainerObject(nodeName, int(container.VMID))
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

	ip := "nil"
	if container.Status == VirtualMachineRunningState {
		if ifaces, err := container.Interfaces(ctx); err == nil {
			if v := firstIPv4FromContainerInterfaces(ifaces); v != "" {
				ip = v
			}
		}
	}
	osInfo := lxcOSInfoFromContainer(container)

	containerStatus := proxmoxv1alpha1.QEMUStatus{
		State:     container.Status,
		Node:      container.Node,
		Uptime:    utils.FormatUptime(int(container.Uptime)),
		ID:        int(container.VMID),
		IPAddress: ip,
		OSInfo:    osInfo,
	}
	return containerStatus, nil
}

func (pc *ProxmoxClient) UpdateContainer(container *proxmoxv1alpha1.Container) error {
	// Get container from proxmox
	containerName := container.Spec.Name
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
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs ||
		container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
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
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	ProxmoxContainer, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return false, err
	}
	// Check if update is needed
	if container.Spec.Template.Cores != ProxmoxContainer.CPUs ||
		container.Spec.Template.Memory != int(ProxmoxContainer.MaxMem/1024/1024) {
		return true, nil
	}
	return false, nil
}

func (pc *ProxmoxClient) IsContainerReady(container *proxmoxv1alpha1.Container) (bool, error) {
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	lxc, err := pc.GetContainer(containerName, nodeName)
	if err != nil {
		return false, err
	}

	if lxc.VMID == 0 {
		return false, nil
	}
	if lxc.ContainerConfig.Lock != "" {
		return false, nil
	}
	return true, nil
}
