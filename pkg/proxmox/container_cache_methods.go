package proxmox

import "github.com/luthermonson/go-proxmox"

func (pc *ProxmoxClient) setCachedContainerID(nodeName, containerName string, containerID int) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	if _, exists := pc.nodesCache[nodeName]; !exists {
		pc.nodesCache[nodeName] = NodeCache{
			vms:        make(map[string]int),
			vmObjs:     make(map[int]*proxmox.VirtualMachine),
			containers: make(map[string]int),
		}
	} else {
		// Ensure maps are initialized (handling existing cache entries)
		entry := pc.nodesCache[nodeName]
		if entry.containers == nil {
			entry.containers = make(map[string]int)
		}
		pc.nodesCache[nodeName] = entry
	}
	pc.nodesCache[nodeName].containers[containerName] = containerID
}

func (pc *ProxmoxClient) getCachedContainerID(nodeName, containerName string) (int, bool) {
	pc.vmIDMutex.RLock()
	defer pc.vmIDMutex.RUnlock()
	if nodeCache, exists := pc.nodesCache[nodeName]; exists && nodeCache.containers != nil {
		id, ok := nodeCache.containers[containerName]
		return id, ok
	}
	return 0, false
}

func (pc *ProxmoxClient) deleteCachedContainer(nodeName, containerName string) {
	pc.vmIDMutex.Lock()
	defer pc.vmIDMutex.Unlock()
	if nodeCache, exists := pc.nodesCache[nodeName]; exists {
		if nodeCache.containers != nil {
			delete(nodeCache.containers, containerName)
		}
	}
}
