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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ContainerSpec defines the desired state of Container
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
// +kubebuilder:validation:XValidation:rule="(has(self.template.name) && self.template.name != \"\") != has(self.template.image)", message="Set exactly one of template.name or template.image"
// +kubebuilder:validation:XValidation:rule="!has(self.template.image) || (size(self.template.disk) > 0)", message="template.disk is required when template.image is set (rootfs volume)"
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
	// +kubebuilder:validation:Required
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

type ContainerTemplate struct {
	// Name of the template container on Proxmox to clone from (mutually exclusive with Image)
	Name string `json:"name,omitempty"`
	// Image pulls an OCI image into Proxmox storage and clones from the resulting template CT.
	// Mutually exclusive with Name.
	Image *ContainerTemplateImage `json:"image,omitempty"`
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

type ContainerTemplateNetwork struct {
	// Name is the name of the network
	Model string `json:"model,omitempty"`
	// Bridge is the name of the bridge
	Bridge string `json:"bridge,omitempty"`
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

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&Container{}, &ContainerList{})
}
