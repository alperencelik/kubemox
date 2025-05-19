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

// VirtualMachineSnapshotSpec defines the desired state of VirtualMachineSnapshot

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
//
//nolint:lll // CEL validation rule is too long
type VirtualMachineSnapshotSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// Name of the virtual machine to take snapshot of
	VirtualMachineName string `json:"virtualMachineName"`
	// Name of the snapshot
	SnapshotName string `json:"snapshotName,omitempty"`
	// Description of the snapshot
	Timestamp metav1.Time `json:"timestamp,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ConnectionRef is immutable"
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

// VirtualMachineSnapshotStatus defines the observed state of VirtualMachineSnapshot
type VirtualMachineSnapshotStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// Possible values: "created", "failed"
	Status string `json:"status,omitempty"`

	// Error message if the snapshot creation process failed
	ErrorMessage string `json:"errorMessage,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VirtualMachineSnapshot is the Schema for the virtualmachinesnapshots API
type VirtualMachineSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSnapshotSpec   `json:"spec,omitempty"`
	Status VirtualMachineSnapshotStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VirtualMachineSnapshotList contains a list of VirtualMachineSnapshot
type VirtualMachineSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineSnapshot `json:"items"`
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachineSnapshot{}, &VirtualMachineSnapshotList{})
}
