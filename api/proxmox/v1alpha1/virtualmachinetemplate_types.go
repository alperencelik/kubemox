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

	// Name is the name of the VM
	Name string `json:"name"`
	// NodeName is the node name
	NodeName string `json:"node"`

	// VirtualMachineConfig is the configuration of the VM
	VirtualMachineConfig VirtualMachineConfig `json:"virtualMachineConfig,omitempty"`

	// Image config
	ImageConfig StorageDownloadURLSpec `json:"imageConfig"`

	// Cloud Init Config
	CloudInitConfig CloudInitConfig `json:"cloudInitConfig,omitempty"`
}

type VirtualMachineConfig struct {
	// Sockets
	// +kubebuilder:default:=1
	Sockets int `json:"sockets,omitempty"`
	// Cores
	// +kubebuilder:default:=2
	Cores int `json:"cores,omitempty"`
	// Memory as MB
	// +kubebuilder:default:=2048
	Memory int `json:"memory,omitempty"`
	// Network
	// +kubebuilder:default:={model:virtio,bridge:vmbr0}
	Network VirtualMachineSpecTemplateNetwork `json:"network,omitempty"`
}

type CloudInitConfig struct {

	// User is the user name for the template
	User string `json:"user,omitempty"`
	// Password is the password for the template
	Password string `json:"password,omitempty"`
	// DNS Domain
	DNSDomain string `json:"dnsDomain,omitempty"`
	// DNS Servers
	DNSServers []string `json:"dnsServers,omitempty"`
	// SSH Keys -- suppose to be on openSSH format
	SSHKeys []string `json:"sshKeys,omitempty"`
	// Upgrade Packages
	// +kubebuilder:default:=true
	UpgradePackages bool `json:"upgradePackages,omitempty"`
	// cicustom --> Take a look in this one
	// ipconfig[n] --> Take a look in this one
}

// VirtualMachineTemplateStatus defines the observed state of VirtualMachineTemplate
type VirtualMachineTemplateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Condition []metav1.Condition `json:"condition,omitempty"`
	Status    string             `json:"status,omitempty"`
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
