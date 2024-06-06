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
	Template VirtualMachineSpecTemplate `json:"template,omitempty"`
	// This field should be modified further
	VMSpec NewVMSpec `json:"vmSpec,omitempty"`
	// DeletionProtection is a flag that indicates whether the VM should be protected from deletion.
	// If true, the VM will not be deleted when the Kubernetes resource is deleted.
	// If not set, it defaults to false.
	// +kubebuilder:default:=false
	DeletionProtection bool `json:"deletionProtection,omitempty"`
	// EnableAutoStart is a flag that indicates whether the VM should automatically start when it's powered off.
	// If true, the VM will start automatically when it's powered off.
	// If not set, it defaults to true.
	// +kubebuilder:default:=true
	EnableAutoStart bool `json:"enableAutoStart,omitempty"`
}

type NewVMSpec struct {
	// Cores is the number of CPU cores
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

type VirtualMachineSpecTemplate struct {
	// Name of the template
	Name string `json:"name,omitempty"`
	// Cores is the number of CPU cores
	Cores int `json:"cores,omitempty"`
	// Socket is the number of CPU sockets
	Socket int `json:"socket,omitempty"`
	// Memory is the amount of memory in MB
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []VirtualMachineSpecTemplateDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network []VirtualMachineSpecTemplateNetwork `json:"network,omitempty"`
}

type VirtualMachineSpecTemplateDisk struct {
	// Storage is the name of the storage
	Storage string `json:"storage"`
	// Size is the size of the disk in GB
	Size int `json:"size"`
	// Device is the name of the device
	Device string `json:"device"`
}

type VirtualMachineSpecTemplateNetwork struct {
	// Model is the model of the network card
	Model string `json:"model"`
	// Bridge is the name of the bridge
	Bridge string `json:"bridge"`
}

type QEMUStatus struct {
	// State is the state of the VM
	State string `json:"state"`
	// Node is the name of the node
	Node string `json:"node"`
	// Uptime is the uptime of the VM
	Uptime string `json:"uptime"`
	// ID is the ID of the VM
	ID int `json:"id"`
	// IPAddress is the IP address of the VM
	IPAddress string `json:"IPAddress"`
	// OSInfo is the OS information of the VM
	OSInfo string `json:"OSInfo"`
}

// VirtualMachineStatus defines the observed state of VirtualMachine
type VirtualMachineStatus struct {
	// Conditions is the metav1.Condition of the Virtual Machine
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
	// Status is the QEMU status of the Virtual Machine (state, node, uptime, id, IP address, os info)
	Status QEMUStatus `json:"status,omitempty"`
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

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachine{}, &VirtualMachineList{})
}
