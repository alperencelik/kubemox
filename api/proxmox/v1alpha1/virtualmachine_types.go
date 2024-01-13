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

// VirtualMachineSpec defines the desired state of VirtualMachine
type VirtualMachineSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name is the name of the VM
	Name string `json:"name"`
	// NodeName is the name of the target node of Proxmox
	NodeName string `json:"nodeName"`
	// TemplateSpec of the source VM
	Template VirtualMachineTemplate `json:"template,omitempty"`
	// This field should be modified further
	VMSpec NewVMSpec `json:"vmSpec,omitempty"`
}

type NewVMSpec struct {
	// CPUs
	Cores int `json:"cores,omitempty"`
	// Memory is the amount of memory in MB
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk NewVMSpecDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network NewVMSpecNetwork `json:"network,omitempty"`
	// OS Image
	OSImage NewVMSpecOSImage `json:"osImage,omitempty"`
}

type NewVMSpecDisk struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type NewVMSpecNetwork struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type NewVMSpecOSImage struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type VirtualMachineTemplate struct {
	// Name of the template
	Name string `json:"name,omitempty"`
	// Cores is the number of CPU cores
	Cores int `json:"cores,omitempty"`
	// Socket is the number of CPU sockets
	Socket int `json:"socket,omitempty"`
	// Memory is the amount of memory in MB
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []VirtualMachineTemplateDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network []VirtualMachineTemplateNetwork `json:"network,omitempty"`
}

type VirtualMachineTemplateDisk struct {
	// Storage is the name of the storage
	Storage string `json:"storage"`
	// Size is the size of the disk in GB
	Size int `json:"size"`
	// Type is the type of the disk
	Type string `json:"type"`
}

type VirtualMachineTemplateNetwork struct {
	// Model is the model of the network card
	Model string `json:"model"`
	// Bridge is the name of the bridge
	Bridge string `json:"bridge"`
}

// VirtualMachineStatus defines the observed state of VirtualMachine
type VirtualMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	State     string `json:"state,omitempty"`
	Node      string `json:"node,omitempty"`
	Name      string `json:"name,omitempty"`
	Uptime    string `json:"uptime,omitempty"`
	ID        int    `json:"id,omitempty"`
	IPAddress string `json:"IPAddress,omitempty"`
	OSInfo    string `json:"OSInfo,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VirtualMachine is the Schema for the virtualmachines API
type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	OwnerReferences   []metav1.OwnerReference `json:"ownerReferences,omitempty"`

	Spec   VirtualMachineSpec   `json:"spec,omitempty"`
	Status VirtualMachineStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VirtualMachineList contains a list of VirtualMachine
type VirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualMachine{}, &VirtualMachineList{})
}
