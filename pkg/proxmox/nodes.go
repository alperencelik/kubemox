package proxmox

import "strings"

func (pc *ProxmoxClient) GetNodes() ([]string, error) {
	// Get all nodes
	nodes, err := pc.Client.Nodes(ctx)
	nodeNames := []string{}
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
				return node.Name, nil
			}
		}
	}
	return "", nil
}
