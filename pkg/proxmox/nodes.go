package proxmox

import (
	"fmt"
	"strings"
)

func (pc *ProxmoxClient) GetNodes() ([]string, error) {
	// Get all nodes
	nodes, err := pc.Client.Nodes(ctx)
	nodeNames := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeNames = append(nodeNames, node.Node)
	}
	if err != nil {
		return nil, err
	}
	return nodeNames, err
}

func (pc *ProxmoxClient) GetOnlineNodes() ([]string, error) {
	nodes, err := pc.Client.Nodes(ctx)
	var OnlineNodes []string
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if node.Status == "online" {
			OnlineNodes = append(OnlineNodes, node.Node)
		}
	}
	return OnlineNodes, nil
}

func (pc *ProxmoxClient) GetNodeOfVM(vmName string) (string, error) {
	nodes, err := pc.GetOnlineNodes()
	if err != nil {
		return "", err
	}
	// Every online node is scanned before answering, rather than returning on
	// the first hit. A name that matches twice is not a machine this function
	// can identify, and returning either one would hand the caller a machine
	// chosen by iteration order.
	var matches []string
	for _, nodeName := range nodes {
		node, err := pc.getNode(ctx, nodeName)
		if err != nil {
			return "", err
		}
		// List VMs on node
		VirtualMachines, err := node.VirtualMachines(ctx)
		if err != nil {
			return "", err
		}
		for _, vm := range VirtualMachines {
			if strings.EqualFold(vm.Name, vmName) {
				// Cache the VM ID while we're here
				pc.setCachedVMID(nodeName, vm.Name, int(vm.VMID))
				matches = append(matches, fmt.Sprintf("%s/vmid %d", node.Name, vm.VMID))
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", &NotFoundError{Message: fmt.Sprintf("virtual machine %q not found on any online node", vmName)}
	case 1:
		return strings.SplitN(matches[0], "/", 2)[0], nil
	default:
		return "", &AmbiguousNameError{Name: vmName, Matches: matches}
	}
}
