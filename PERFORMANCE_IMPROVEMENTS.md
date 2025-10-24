# Performance Improvements - Proxmox API Call Reduction

## Overview

This document describes the performance improvements made to reduce the number of Proxmox API calls made by the kubemox operator.

## Problem

The original implementation was making multiple redundant API calls for a single operation. For example, to get a single virtual machine:

### Before (4+ API calls for one VM):
```go
func (pc *ProxmoxClient) getVirtualMachine(vmName, nodeName string) (*proxmox.VirtualMachine, error) {
    node, err := pc.Client.Node(ctx, nodeName)  // API call 1: GET /nodes/{node}/status
    if err != nil {
        return nil, err
    }
    vmID, err := pc.getVMID(vmName, nodeName)  // Makes 2 more API calls:
    // - API call 2: GET /nodes/{node}/status (duplicate!)
    // - API call 3: GET /nodes/{node}/qemu (lists ALL VMs to find ID)
    if err != nil {
        return nil, err
    }
    VirtualMachine, err := node.VirtualMachine(ctx, vmID)  // Makes 2 more API calls:
    // - API call 4: GET /nodes/{node}/qemu/{vmid}/status/current
    // - API call 5: GET /nodes/{node}/qemu/{vmid}/config
    if err != nil {
        return nil, err
    }
    return VirtualMachine, nil
}
```

**Total: 5 API calls** (with 1 duplicate) to get information about a single VM.

## Solution

Implemented a caching mechanism in the `ProxmoxClient` struct to reduce redundant API calls:

### Cache Structure
```go
type ProxmoxClient struct {
    Client     *proxmox.Client
    // Cache for nodes to reduce API calls
    nodesCache map[string]*proxmox.Node
    nodesMutex sync.RWMutex
    // Cache for VM IDs to reduce API calls
    vmIDCache  map[string]map[string]int // nodeName -> vmName -> vmID
    vmIDMutex  sync.RWMutex
}
```

### After (2-3 API calls, or 0 if cached):

1. **First call** (cache miss):
   - API call 1: GET /nodes/{node}/status (cached)
   - API call 2: GET /nodes/{node}/qemu (VM ID cached)
   - API call 3: GET /nodes/{node}/qemu/{vmid}/status/current
   - API call 4: GET /nodes/{node}/qemu/{vmid}/config

2. **Subsequent calls** (cache hit):
   - API call 1: GET /nodes/{node}/qemu/{vmid}/status/current
   - API call 2: GET /nodes/{node}/qemu/{vmid}/config

**Total reduction: From 5 calls to 2 calls** (60% reduction)

## Key Improvements

### 1. Node Caching
- Node objects are cached on first access
- Eliminates redundant `/nodes/{node}/status` calls
- Thread-safe with RWMutex

### 2. VM ID Caching
- VM IDs are cached when discovered
- Eliminates need to list all VMs to find a specific VM ID
- Automatically populated during:
  - VM creation
  - VM listing operations
  - Node VM enumeration

### 3. Cache Invalidation
- VM IDs are removed from cache when VMs are deleted
- Ensures cache consistency

## Impact on Operations

### VM Operations
- **GetVirtualMachine**: 5 calls → 2 calls (60% reduction)
- **CheckVM**: 3 calls → 0-1 calls (when cached)
- **GetVMID**: 2 calls → 0 calls (when cached)
- **CreateVM**: Now caches new VM ID immediately
- **DeleteVM**: Invalidates cache entry

### Container Operations
- Similar improvements for all container operations
- CloneContainer, GetContainer, etc. all benefit from node caching

### Storage Operations
- StorageDownloadURL benefits from node caching
- GetStorageContent benefits from node caching

### Certificate Operations
- CreateCustomCertificate benefits from node caching
- DeleteCustomCertificate benefits from node caching

## Performance Benefits

### Latency Reduction
- **Single VM query**: ~60% reduction in API calls
- **Multiple operations**: Cumulative benefits as cache warms up
- **Network latency**: Reduced impact when operator and Proxmox have high latency

### Scale Improvements
- **Large clusters**: Fewer API calls means better scalability
- **High-frequency operations**: Watchers and reconciliation loops benefit significantly
- **API rate limits**: Reduced risk of hitting Proxmox API rate limits

### Eventually Consistent Model
- Cache makes the operator more eventually consistent
- Aligns with Kubernetes reconciliation model
- Reduces unnecessary API calls during watch operations

## Future Enhancements (Optional)

Potential areas for further optimization:

1. **Cache TTL/Expiry**: Add time-based cache expiration
2. **Cache Warm-up**: Pre-populate cache on client initialization
3. **VM State Caching**: Cache VM status information with short TTL
4. **Batch Operations**: Fetch multiple VMs in a single operation where possible
5. **Event-based Invalidation**: Use Proxmox events to invalidate cache entries

## Testing Recommendations

1. Test VM lifecycle operations (create, update, delete)
2. Test container lifecycle operations
3. Test with multiple concurrent operations
4. Test cache invalidation on VM deletion
5. Test behavior under network latency
6. Monitor Proxmox API call patterns in production

## Conclusion

The caching implementation significantly reduces the number of Proxmox API calls while maintaining correctness. This makes the operator more performant, especially under scale and network latency conditions, while moving toward an eventually consistent model that better aligns with Kubernetes patterns.
