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
	// Name of the template
	Name string `json:"name,omitempty"`
	// Cores is the number of CPU cores
	Cores int `json:"cores,omitempty"`
	// Memory is the amount of memory in MB
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []ContainerTemplateDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network []ContainerTemplateNetwork `json:"network,omitempty"`
}

type ContainerTemplateDisk struct {
	// Storage is the name of the storage
	Storage string `json:"storage,omitempty"`
	// Size is the size of the disk
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
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope="Cluster",shortName="lxc"
//+kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="The name of the target node of Proxmox"
//+kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.status.id",description="The ID of the container"
//+kubebuilder:printcolumn:name="Cores",type="string",JSONPath=".spec.template.cores",description="The number of CPU cores"
//+kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.template.memory",description="The amount of memory in MB"
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.status.state",description="The state of the VM"
//+kubebuilder:printcolumn:name="Uptime",type="string",JSONPath=".status.status.uptime",description="The uptime of the VM"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Container is the Schema for the containers API
type Container struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerSpec   `json:"spec,omitempty"`
	Status ContainerStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ContainerList contains a list of Container
type ContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Container `json:"items"`
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&Container{}, &ContainerList{})
}
