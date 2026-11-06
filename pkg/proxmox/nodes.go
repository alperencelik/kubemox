package proxmox

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
