package proxmox

import "strings"

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
	return nodeNames, err
}

func GetOnlineNodes() ([]string, error) {
	nodes, err := Client.Nodes(ctx)
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

func GetNodeOfVM(vmName string) (string, error) {
	nodes, err := GetOnlineNodes()
	if err != nil {
		return "", err
	}
	for _, node := range nodes {
		node, err := Client.Node(ctx, node)
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
				return node.Name, nil
			}
		}
	}
	return "", nil
}
