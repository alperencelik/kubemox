/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ContainerSpec defines the desired state of Container
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
// +kubebuilder:validation:XValidation:rule="[has(self.template.name) && self.template.name != \"\", has(self.template.image), has(self.template.osTemplate) && self.template.osTemplate != \"\"].filter(x, x).size() == 1", message="Set exactly one of template.name, template.image or template.osTemplate"
// +kubebuilder:validation:XValidation:rule="!(has(self.template.image) || has(self.template.osTemplate)) || (has(self.template.disk) && size(self.template.disk) > 0)", message="template.disk is required when template.image or template.osTemplate is set (rootfs volume)"
// +kubebuilder:validation:XValidation:rule="!has(self.template.name) || self.template.name == \"\" || ((!has(self.template.sshPublicKeys) || size(self.template.sshPublicKeys) == 0) && !(has(self.template.unprivileged) && self.template.unprivileged))", message="template.sshPublicKeys and template.unprivileged apply on create only; a clone (template.name) inherits them from its template"
// +kubebuilder:validation:XValidation:rule="!has(self.template.image) || (has(self.template.image.reference) && self.template.image.reference != \"\" && has(self.template.image.storage) && self.template.image.storage != \"\")", message="template.image.reference and template.image.storage are required when template.image is set"
//
//nolint:lll // CEL validation rule is too long
type ContainerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name is the name of the Container
	Name string `json:"name"`
	// NodeName is the name of the target node of Proxmox
	NodeName string `json:"nodeName"`
	// TemplateSpec of the source Container
	Template ContainerTemplate `json:"template,omitempty"`
	// DeletionProtection is a flag that indicates whether the resource should be protected from deletion.
	// If true, the resource will not be deleted when the Kubernetes resource is deleted.
	// If not set, it defaults to false.
	// +kubebuilder:default:=false
	DeletionProtection bool `json:"deletionProtection,omitempty"`
	// EnableAutoStart is a flag that indicates whether the resource should automatically start when it's powered off.
	// If true, the resource will start automatically when it's powered off.
	// If not set, it defaults to true.
	// +kubebuilder:default:=true
	EnableAutoStart bool `json:"enableAutoStart,omitempty"`
	// CloneMode selects how the template container is cloned: Full copies it,
	// Linked references the template's base snapshot and pins the template for
	// as long as the clone exists.
	// +kubebuilder:validation:Enum=Full;Linked
	// +kubebuilder:default:=Full
	CloneMode CloneMode `json:"cloneMode,omitempty"`
	// +kubebuilder:validation:Required
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

type ContainerTemplate struct {
	// Name of the template container on Proxmox to clone from (mutually exclusive with Image and OSTemplate)
	Name string `json:"name,omitempty"`
	// Image pulls an OCI image into Proxmox storage and clones from the resulting template CT.
	// Mutually exclusive with Name and OSTemplate.
	Image *ContainerTemplateImage `json:"image,omitempty"`
	// OSTemplate is a volume to create the container from: a container template archive or an OCI
	// image archive that is already on a storage, e.g. local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst.
	// Mutually exclusive with Name and Image.
	OSTemplate string `json:"osTemplate,omitempty"`
	// Unprivileged creates the container with its root mapped to an unprivileged user on the host.
	// Applied on create only.
	Unprivileged bool `json:"unprivileged,omitempty"`
	// Features is the Proxmox features string, for example nesting=1,keyctl=1.
	Features string `json:"features,omitempty"`
	// SSHPublicKeys are written to root's authorized_keys on create.
	SSHPublicKeys []string `json:"sshPublicKeys,omitempty"`
	// Nameserver is the DNS server, or several separated by spaces. Default: the host's.
	Nameserver string `json:"nameserver,omitempty"`
	// SearchDomain is the DNS search domain. Default: the host's.
	SearchDomain string `json:"searchDomain,omitempty"`
	// Cores is the number of CPU cores
	// +kubebuilder:validation:Minimum=1
	Cores int `json:"cores,omitempty"`
	// Memory is the amount of memory in MB
	// +kubebuilder:validation:Minimum=16
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []ContainerTemplateDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network []ContainerTemplateNetwork `json:"network,omitempty"`
}

// ContainerTemplateImage is an OCI image and the Proxmox datastore used for oci-registry-pull.
type ContainerTemplateImage struct {
	// Reference is the OCI image (for example nginx:alpine).
	// +kubebuilder:validation:MinLength=1
	Reference string `json:"reference"`
	// Storage is the Proxmox storage ID where the image is pulled and stored. It must be file-based
	// (dir, nfs, zfs, ...); lvmthin cannot store OCI blobs. template.disk[0].storage is only for rootfs.
	// +kubebuilder:validation:MinLength=1
	Storage string `json:"storage"`
}

type ContainerTemplateDisk struct {
	// Storage is the name of the storage
	Storage string `json:"storage,omitempty"`
	// Size is the size of the disk
	// +kubebuilder:validation:Minimum=1
	Size int `json:"size,omitempty"`
	// Type is the type of the disk
	Type string `json:"type,omitempty"`
}

// ContainerTemplateNetwork is one NIC: the first is eth0, the next eth1, and so on.
type ContainerTemplateNetwork struct {
	// Name is the name of the network
	Model string `json:"model,omitempty"`
	// Bridge is the name of the bridge
	Bridge string `json:"bridge,omitempty"`
	// VLAN is the Proxmox VLAN tag (1-4094) of the NIC. Omit for untagged traffic on the bridge.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4094
	VLAN *int32 `json:"vlan,omitempty"`
	// IP is the IPv4 address in CIDR notation, dhcp, or manual. Default: dhcp.
	// +kubebuilder:validation:Pattern=`^(dhcp|manual|([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2})$`
	IP string `json:"ip,omitempty"`
	// Gateway is the IPv4 default gateway of the NIC.
	Gateway string `json:"gateway,omitempty"`
	// MTU of the NIC. Default: the bridge's.
	// +kubebuilder:validation:Minimum=64
	// +kubebuilder:validation:Maximum=65535
	MTU int `json:"mtu,omitempty"`
	// Firewall enables the Proxmox firewall on the NIC.
	Firewall bool `json:"firewall,omitempty"`
}

// ContainerStatus defines the observed state of Container
type ContainerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
	Status     QEMUStatus         `json:"status,omitempty"`
	// AppliedImageReference is the OCI reference the running Proxmox CT was created from (template.image only).
	// If spec.template.image.reference (or storage) diverges, the controller replaces the CT.
	// +optional
	AppliedImageReference string `json:"appliedImageReference,omitempty"`
	// AppliedImageStorage is the OCI blob storage ID used when the CT was created (template.image only).
	// +optional
	AppliedImageStorage string `json:"appliedImageStorage,omitempty"`
	// LastObserved is the timestamp of the last successful observation of the
	// Container state from Proxmox. A stale value relative to wall-clock time
	// indicates the operator has lost the ability to verify .status.status
	// against Proxmox.
	// +optional
	LastObserved *metav1.Time `json:"lastObserved,omitempty"`
	// ObservedGeneration is the .metadata.generation that the controller last
	// reconciled successfully.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope="Cluster",shortName="lxc"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="The name of the target node of Proxmox"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.status.id",description="The ID of the container"
// +kubebuilder:printcolumn:name="Cores",type="string",JSONPath=".spec.template.cores",description="The number of CPU cores"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.template.memory",description="The amount of memory in MB"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.status.state",description="The state of the VM"
// +kubebuilder:printcolumn:name="Uptime",type="string",JSONPath=".status.status.uptime",description="The uptime of the VM"
// +kubebuilder:printcolumn:name="Last Observed",type="date",JSONPath=".status.lastObserved",description="The last time the Container state was observed from Proxmox"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Container is the Schema for the containers API
type Container struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerSpec   `json:"spec,omitempty"`
	Status ContainerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ContainerList contains a list of Container
type ContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Container `json:"items"`
}

// GetConditions returns the status conditions of the Container.
func (c *Container) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the status conditions of the Container.
func (c *Container) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

// MarkObserved stamps LastObserved with the supplied time and records the
// generation that produced the current status. Call only after a successful
// observation against Proxmox so consumers can distinguish fresh status from
// status that the operator could not re-verify.
func (s *ContainerStatus) MarkObserved(t time.Time, generation int64) {
	now := metav1.NewTime(t)
	s.LastObserved = &now
	s.ObservedGeneration = generation
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&Container{}, &ContainerList{})
}
