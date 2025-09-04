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

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ProxmoxConnectionSpec defines the desired state of ProxmoxConnection.
type ProxmoxConnectionSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// Endpoint is the address of the Proxmox cluster endpoint
	Endpoint string `json:"endpoint"`
	// Username to authenticate with the Proxmox cluster
	Username string `json:"username,omitempty"`
	// Password to authenticate with the Proxmox cluster
	Password string `json:"password,omitempty"`
	// TokenID to authenticate with the Proxmox cluster
	TokenID string `json:"tokenID,omitempty"`
	// Secret to authenticate with the Proxmox cluster
	Secret string `json:"secret,omitempty"`
	// InsecureSkipVerify skips the verification of the server's certificate chain and host name
	// +kubebuilder:default:=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// ProxmoxConnectionStatus defines the observed state of ProxmoxConnection.
type ProxmoxConnectionStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
	ConnectionStatus string             `json:"connectionStatus,omitempty"`
	Version          string             `json:"version,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope="Cluster",shortName="pxc"

// ProxmoxConnection is the Schema for the proxmoxconnections API.
type ProxmoxConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProxmoxConnectionSpec   `json:"spec,omitempty"`
	Status ProxmoxConnectionStatus `json:"status,omitempty"`
}

// ProxmoxConnectionList contains a list of ProxmoxConnection.
// +kubebuilder:object:root=true
type ProxmoxConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProxmoxConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProxmoxConnection{}, &ProxmoxConnectionList{})
}
