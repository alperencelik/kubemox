package proxmox

import (
	"errors"
	"fmt"
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

// isContainerGoneErr reports whether err says the container does not exist:
// either no container has the name, or Proxmox has no config for the VMID
// ("Configuration file 'nodes/<node>/lxc/<vmid>.conf' does not exist").
func isContainerGoneErr(err error) bool {
	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "configuration file") && strings.Contains(msg, "does not exist")
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
	if cc.Memory != nil && *cc.Memory > 0 {
		c.MaxMem = uint64(*cc.Memory) * 1024 * 1024
	}
	setProxmoxContainerClient(c, pc.Client)
	return c, nil
}

func firstIPv4FromContainerInterfaces(ifaces proxmox.ContainerInterfaces) string {
	for _, iface := range ifaces {
		if strings.EqualFold(iface.Name, "lo") {
			continue
		}
		if v := utils.ParseIPv4FromLXCInet(iface.Inet); v != "" {
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

// CloneContainer clones the container from its template, tags it with the
// object's UID and returns its VMID.
func (pc *ProxmoxClient) CloneContainer(container *proxmoxv1alpha1.Container) (int, error) {
	// Returning an error is quite reasonable here since that error moved up
	// to the controller and will be handled there as requeue
	containerName := container.Spec.Name
	nodeName := container.Spec.NodeName
	if _, err := pc.getNode(ctx, nodeName); err != nil {
		return 0, err
	}
	if container.Spec.Template.Image != nil {
		return pc.CreateContainerFromOCIImage(container)
	}
	if container.Spec.Template.OSTemplate != "" {
		return pc.createContainerFromVolume(container, container.Spec.Template.OSTemplate)
	}
	templateContainerName := TemplateContainerName(container.Spec.Template)
	named, err := pc.containersNamed(templateContainerName, nodeName)
	if err != nil {
		return 0, err
	}
	if len(named) != 1 {
		return 0, fmt.Errorf("template container %s: %d containers on node %s have that name, want 1",
			templateContainerName, len(named), nodeName)
	}
	templateContainer, err := pc.getContainerByID(nodeName, int(named[0].VMID))
	if err != nil {
		return 0, err
	}

	var CloneOptions proxmox.ContainerCloneOptions
	CloneOptions.Full = proxmox.IntOrBool(container.Spec.CloneMode.IsFullClone())
	CloneOptions.Hostname = containerName
	CloneOptions.Target = nodeName
	log.Log.Info(fmt.Sprintf("Cloning container %s from template %s", containerName, templateContainerName))

	newID, task, err := templateContainer.Clone(ctx, &CloneOptions)
	if err != nil {
		log.Log.Error(err, "Can't clone container")
		return 0, err
	}
	taskStatus, taskCompleted, taskWaitErr := task.WaitForCompleteStatus(ctx, 5, 10)
	if !taskStatus {
		// Return the task.ExitStatus as an error
		return 0, &TaskError{ExitStatus: task.ExitStatus}
	}
	if !taskCompleted {
		return 0, taskWaitErr
	}
	clone, err := pc.getContainerByID(nodeName, newID)
	if err != nil {
		return 0, err
	}
	// The clone carries the template's tags; a kubemox-uid tag among them is replaced.
	if _, err = clone.Config(ctx, proxmox.ContainerOption{
		Name: containerOptionTags, Value: withContainerUIDTag(containerTags(templateContainer), container),
	}); err != nil {
		return 0, fmt.Errorf("tagging container %s (vmid %d): %w", containerName, newID, err)
	}
	if err = pc.applyCloneSettings(clone, container); err != nil {
		return 0, err
	}
	return newID, nil
}

// containersNamed lists the containers on the node whose hostname is name.
// Proxmox does not keep hostnames unique, so there may be several.
func (pc *ProxmoxClient) containersNamed(name, nodeName string) ([]*proxmox.Container, error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	containers, err := node.Containers(ctx)
	if err != nil {
		return nil, err
	}
	var named []*proxmox.Container
	for _, c := range containers {
		if c.Name == name {
			named = append(named, c)
		}
	}
	return named, nil
}

func (pc *ProxmoxClient) ContainerExists(container *proxmoxv1alpha1.Container) (bool, error) {
	_, err := pc.GetContainer(container)
	if isContainerGoneErr(err) {
		return false, nil
	}
	return err == nil, err
}

// GetContainer returns the Proxmox container that belongs to the object,
// read from the API. It is addressed by status.status.id once kubemox has
// recorded it; the hostname is looked up only before that. Either way a
// container tagged for another object fails with NameConflictError.
func (pc *ProxmoxClient) GetContainer(ct *proxmoxv1alpha1.Container) (*proxmox.Container, error) {
	id := ct.Status.Status.ID
	if id == 0 {
		named, err := pc.containersNamed(ct.Spec.Name, ct.Spec.NodeName)
		if err != nil {
			return nil, err
		}
		if id, err = pickContainerByName(ct, named); err != nil {
			return nil, err
		}
	}
	container, err := pc.getContainerByID(ct.Spec.NodeName, id)
	if err != nil {
		return nil, err
	}
	if err = checkContainerOwner(ct, container); err != nil {
		return nil, err
	}
	return container, nil
}

func (pc *ProxmoxClient) getContainerByID(nodeName string, containerID int) (*proxmox.Container, error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	container, err := node.Container(ctx, containerID)
	if isLXCNotRunningErr(err) {
		container, err = pc.lxcFromConfigWhenStopped(nodeName, containerID)
	}
	if isContainerGoneErr(err) {
		return nil, &NotFoundError{Message: fmt.Sprintf("container %d not found on node %s: %v", containerID, nodeName, err)}
	}
	if err != nil {
		return nil, err
	}
	return container, nil
}

// ClaimContainer returns the VMID of the object's container and tags it with
// the object's UID when it carries no kubemox-uid tag yet.
func (pc *ProxmoxClient) ClaimContainer(ct *proxmoxv1alpha1.Container) (int, error) {
	container, err := pc.GetContainer(ct)
	if err != nil {
		return 0, err
	}
	if containerOwnerUID(containerTags(container)) == "" {
		log.Log.Info(fmt.Sprintf("Adopting container %s (vmid %d)", ct.Spec.Name, container.VMID))
		if _, err = container.Config(ctx, proxmox.ContainerOption{
			Name: containerOptionTags, Value: withContainerUIDTag(containerTags(container), ct),
		}); err != nil {
			return 0, fmt.Errorf("tagging container %s (vmid %d): %w", ct.Spec.Name, container.VMID, err)
		}
	}
	return int(container.VMID), nil
}

func (pc *ProxmoxClient) StopContainer(ct *proxmoxv1alpha1.Container) error {
	// Get container
	log.Log.Info(fmt.Sprintf("Stopping container %s", ct.Spec.Name))
	container, err := pc.GetContainer(ct)
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

// DeleteContainer stops and destroys the object's container. A container
// that is already gone counts as deleted, and so does one that belongs to
// another object, which is left alone: the caller can drop its finalizer.
func (pc *ProxmoxClient) DeleteContainer(ct *proxmoxv1alpha1.Container) error {
	containerName := ct.Spec.Name
	container, err := pc.GetContainer(ct)
	switch {
	case isContainerGoneErr(err):
		log.Log.Info(fmt.Sprintf("Container %s is already deleted", containerName))
		return nil
	case IsNameConflict(err):
		log.Log.Info(fmt.Sprintf("Not deleting container %s: %v", containerName, err))
		return nil
	case err != nil:
		return err
	}
	if container.Status == VirtualMachineRunningState {
		if err = pc.StopContainer(ct); err != nil {
			return err
		}
	}
	log.Log.Info(fmt.Sprintf("Deleting container %s (vmid %d)", containerName, container.VMID))
	mutex.Lock()
	defer mutex.Unlock()
	task, err := container.Delete(ctx, nil)
	switch {
	case isContainerGoneErr(err):
	case err != nil:
		return err
	default:
		taskStatus, taskCompleted, taskErr := task.WaitForCompleteStatus(ctx, 5, 5)
		if !taskCompleted {
			if taskErr != nil {
				return taskErr
			}
			return fmt.Errorf("timeout waiting for container %s delete task to finish", containerName)
		}
		if !taskStatus {
			return &TaskError{ExitStatus: task.ExitStatus}
		}
	}
	log.Log.Info(fmt.Sprintf("Container %s has been deleted", containerName))
	return nil
}

func (pc *ProxmoxClient) StartContainer(ct *proxmoxv1alpha1.Container) error {
	container, err := pc.GetContainer(ct)
	if err != nil {
		return err
	}
	if container.Status == VirtualMachineRunningState {
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
		return fmt.Errorf("timeout waiting for container %s start task to finish", ct.Spec.Name)
	}
	if !taskStatus {
		return &TaskError{ExitStatus: taskID.ExitStatus}
	}
	log.Log.Info(fmt.Sprintf("Container %s has been started", ct.Spec.Name))
	return nil
}

func (pc *ProxmoxClient) GetContainerState(ct *proxmoxv1alpha1.Container) (string, error) {
	container, err := pc.GetContainer(ct)
	if err != nil {
		return "", err
	}
	return container.Status, nil
}

func (pc *ProxmoxClient) UpdateContainerStatus(ct *proxmoxv1alpha1.Container) (proxmoxv1alpha1.QEMUStatus, error) {
	container, err := pc.GetContainer(ct)
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
	var cpuOption proxmox.ContainerOption
	var memoryOption proxmox.ContainerOption
	cpuOption.Name = virtualMachineCPUOption
	memoryOption.Name = virtualMachineMemoryOption
	ProxmoxContainer, err := pc.GetContainer(container)
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
	return updateContainerNets(ProxmoxContainer, container)
}

func (pc *ProxmoxClient) RestartContainer(ct *proxmoxv1alpha1.Container) (bool, error) {
	// Get container
	container, err := pc.GetContainer(ct)
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
		contStatus, errr := pc.GetContainerState(ct)
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
	ProxmoxContainer, err := pc.GetContainer(container)
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
	lxc, err := pc.GetContainer(container)
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
