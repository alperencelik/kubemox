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

// VirtualMachineSetSpec defines the desired state of VirtualMachineSet
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
//
//nolint:lll // CEL validation rule is too long
type VirtualMachineSetSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

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
	// Replicas is the number of VMs
	Replicas int `json:"replicas"`
	// NodeName is the name of the target node of Proxmox
	NodeName string `json:"nodeName"`
	// Template is the name of the source VM template
	Template VirtualMachineSpecTemplate `json:"template,omitempty"`
	// AdditionalConfig is the additional configuration of the VM
	// +kubebuilder:validation:Optional
	AdditionalConfig map[string]string `json:"additionalConfig,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
	//
	//nolint:lll // CEL validation rule is too long
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

// VirtualMachineSetStatus defines the observed state of VirtualMachineSet
type VirtualMachineSetStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope="Cluster",shortName=vmset
//+kubebuilder:printcolumn:name="Replicas",type="integer",JSONPath=".spec.replicas",description="The number of VMs"
//+kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="The name of the target node of Proxmox"
//+kubebuilder:printcolumn:name="Cores",type="string",JSONPath=".spec.template.cores",description="The number of CPU cores"
//+kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.template.memory",description="The amount of memory in MB"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].type",description="The status of the VM"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VirtualMachineSet is the Schema for the virtualmachinesets API
type VirtualMachineSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSetSpec   `json:"spec,omitempty"`
	Status VirtualMachineSetStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VirtualMachineSetList contains a list of VirtualMachineSet
type VirtualMachineSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineSet `json:"items"`
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachineSet{}, &VirtualMachineSetList{})
}
