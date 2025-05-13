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

// VirtualMachineSnapshotPolicySpec defines the desired state of VirtualMachineSnapshotPolicy
type VirtualMachineSnapshotPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	NamespaceSelector NamespaceSelector `json:"namespaceSelector,omitempty"`

	LabelSelector metav1.LabelSelector `json:"labelSelector,omitempty"`

	SnapshotSchedule string `json:"snapshotSchedule,omitempty"`
	// +kubebuilder:validation:Optional
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

type NamespaceSelector struct {
	Namespaces []string `json:"namespaces,omitempty"`
}

// VirtualMachineSnapshotPolicyStatus defines the observed state of VirtualMachineSnapshotPolicy
type VirtualMachineSnapshotPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// VirtualMachineSnapshotPolicy is the Schema for the virtualmachinesnapshotpolicies API
type VirtualMachineSnapshotPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSnapshotPolicySpec   `json:"spec,omitempty"`
	Status VirtualMachineSnapshotPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// VirtualMachineSnapshotPolicyList contains a list of VirtualMachineSnapshotPolicy
type VirtualMachineSnapshotPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineSnapshotPolicy `json:"items"`
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachineSnapshotPolicy{}, &VirtualMachineSnapshotPolicyList{})
}
