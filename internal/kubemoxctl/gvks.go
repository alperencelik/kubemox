package kubemoxctl

import "k8s.io/apimachinery/pkg/runtime/schema"

// Group and version for every kubemox CRD.
const (
	Group   = "proxmox.alperen.cloud"
	Version = "v1alpha1"
)

// ResourceType pairs a GroupVersionResource with the Kind it represents.
// Keeping both explicit avoids ad-hoc pluralization rules (e.g. "Set" → "Sets"
// but "Policy" → "Policies").
type ResourceType struct {
	GVR  schema.GroupVersionResource
	Kind string
}

// ExportOrder lists every kubemox kind in the order it must be created on the
// target cluster. Parents appear before their children so ownerReferences
// rewriting always finds the parent UID already populated.
var ExportOrder = []ResourceType{
	{gvr("proxmoxconnections"), "ProxmoxConnection"},
	{gvr("virtualmachinetemplates"), "VirtualMachineTemplate"},
	{gvr("storagedownloadurls"), "StorageDownloadURL"},
	{gvr("virtualmachinesets"), "VirtualMachineSet"},
	{gvr("virtualmachines"), "VirtualMachine"},
	{gvr("managedvirtualmachines"), "ManagedVirtualMachine"},
	{gvr("virtualmachinesnapshots"), "VirtualMachineSnapshot"},
	{gvr("virtualmachinesnapshotpolicies"), "VirtualMachineSnapshotPolicy"},
	{gvr("containers"), "Container"},
	{gvr("customcertificates"), "CustomCertificate"},
}

func gvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: Group, Version: Version, Resource: resource}
}

// APIVersion returns the fully-qualified apiVersion string ("group/version").
func APIVersion() string {
	return Group + "/" + Version
}
