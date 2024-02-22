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

// CustomCertificateSpec defines the desired state of CustomCertificate
type CustomCertificateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	NodeName        string          `json:"nodeName,omitempty"`
	CertManagerSpec CertManagerSpec `json:"certManagerSpec,omitempty"`
	ProxmoxCertSpec ProxmoxCertSpec `json:"proxmoxCertSpec,omitempty"`
}

type CertManagerSpec struct {
	CommonName string    `json:"commonName,omitempty"`
	DNSNames   []string  `json:"dnsNames,omitempty"`
	IssuerRef  IssuerRef `json:"issuerRef,omitempty"`
	SecretName string    `json:"secretName,omitempty"`
	Usages     []string  `json:"usages,omitempty"`
}

type IssuerRef struct {
	Name  string `json:"name,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Group string `json:"group,omitempty"`
}

type ProxmoxCertSpec struct {
	Certificate  string `json:"certificate,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	NodeName     string `json:"nodeName,omitempty"`
	Force        bool   `json:"force,omitempty"`
	RestartProxy bool   `json:"restartProxy,omitempty"`
}

// CustomCertificateStatus defines the observed state of CustomCertificate
type CustomCertificateStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Status string `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// CustomCertificate is the Schema for the customcertificates API
type CustomCertificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CustomCertificateSpec   `json:"spec,omitempty"`
	Status CustomCertificateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CustomCertificateList contains a list of CustomCertificate
type CustomCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CustomCertificate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CustomCertificate{}, &CustomCertificateList{})
}
