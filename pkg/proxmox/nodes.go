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
