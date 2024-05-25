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

// ManagedVirtualMachineSpec defines the desired state of ManagedVirtualMachine
type ManagedVirtualMachineSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name is the name of the ManagedVirtualMachine
	Name string `json:"name"`
	// NodeName is the name of the node where the ManagedVirtualMachine has exists
	NodeName string `json:"nodeName"`
	// Cores is the number of cores of the ManagedVirtualMachine
	Cores int `json:"cores"`
	// Memory is the amount of memory in MB of the ManagedVirtualMachine
	Memory int `json:"memory"`
	// Disk is the amount of disk in GB of the ManagedVirtualMachine
	Disk int `json:"disk"`
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

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ManagedVirtualMachine is the Schema for the managedvirtualmachines API
type ManagedVirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManagedVirtualMachineSpec `json:"spec,omitempty"`
	Status VirtualMachineStatus      `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ManagedVirtualMachineList contains a list of ManagedVirtualMachine
type ManagedVirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedVirtualMachine `json:"items"`
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&ManagedVirtualMachine{}, &ManagedVirtualMachineList{})
}
