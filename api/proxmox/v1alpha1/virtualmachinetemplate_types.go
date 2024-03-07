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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VirtualMachineTemplateSpec defines the desired state of VirtualMachineTemplate
type VirtualMachineTemplateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// CPU cores for the VM
	CPUCores int `json:"cpuCores,omitempty"`
	// Socket for the VM
	Socket int `json:"socket,omitempty"`
	// Memory size for the VM in GB
	MemorySize int `json:"memorySize,omitempty"`
	// Disk size for the VM in GB
	DiskSize int `json:"diskSize,omitempty"`
	// Network interface for the VM
	NetworkInterface string `json:"networkInterface,omitempty"`
	// Image for the VM
	Image string `json:"image,omitempty"`
	// Cloud-init configuration for the VM
	CloudInit CloudInit `json:"cloudInit,omitempty"`
}

type CloudInit struct {
	// User data for the cloud-init
	UserData string `json:"userData"`
	// Meta data for the cloud-init
	MetaData string `json:"metaData"`
}

// VirtualMachineTemplateStatus defines the observed state of VirtualMachineTemplate
type VirtualMachineTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VirtualMachineTemplate is the Schema for the virtualmachinetemplates API
type VirtualMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineTemplateSpec   `json:"spec,omitempty"`
	Status VirtualMachineTemplateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VirtualMachineTemplateList contains a list of VirtualMachineTemplate
type VirtualMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualMachineTemplate{}, &VirtualMachineTemplateList{})
}
