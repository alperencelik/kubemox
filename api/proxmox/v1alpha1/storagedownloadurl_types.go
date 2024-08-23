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

// StorageDownloadURLSpec defines the desired state of StorageDownloadURL
type StorageDownloadURLSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Pattern=\b(iso|vztmpl)\b
	Content  string `json:"content"`
	Filename string `json:"filename"`
	Node     string `json:"node"`
	// +kubebuilder:default:=local
	Storage string `json:"storage,omitempty"`
	URL     string `json:"url"`
	// Optional fields
	Checksum          string `json:"checksum,omitempty"`
	ChecksumAlgorithm string `json:"checksumAlgorithm,omitempty"`
	Compression       string `json:"compression,omitempty"`
	VerifyCertificate bool   `json:"verifyCertificate,omitempty"`
}

// StorageDownloadURLStatus defines the observed state of StorageDownloadURL
type StorageDownloadURLStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
	Status     string             `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// StorageDownloadURL is the Schema for the storagedownloadurls API
type StorageDownloadURL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StorageDownloadURLSpec   `json:"spec,omitempty"`
	Status StorageDownloadURLStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// StorageDownloadURLList contains a list of StorageDownloadURL
type StorageDownloadURLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StorageDownloadURL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&StorageDownloadURL{}, &StorageDownloadURLList{})
}
