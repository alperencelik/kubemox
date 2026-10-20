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

// VirtualMachineVolumeSpec defines the desired state of VirtualMachineVolume.
//
// A VirtualMachineVolume is a Proxmox volume that outlives the virtual machine
// it is attached to. Proxmox has no standalone volume: every volume belongs
// to a VMID, and destroying a VM destroys the volumes still attached to it.
// So the disk lives on a holder, a VM per namespace and node that never
// starts, and moves between the holder and a VirtualMachine with Proxmox's
// disk reassignment. Reassignment only works between VMs on the same node,
// which is why the node is part of the spec and immutable.
//
// +kubebuilder:validation:XValidation:rule="self.nodeName == oldSelf.nodeName",message="nodeName is immutable: a volume cannot move to another node"
// +kubebuilder:validation:XValidation:rule="self.storage == oldSelf.storage",message="storage is immutable: a volume cannot move to another storage"
// +kubebuilder:validation:XValidation:rule="has(self.source) == has(oldSelf.source) && (!has(self.source) || self.source == oldSelf.source)",message="source is immutable: it only populates the disk at creation"
// +kubebuilder:validation:XValidation:rule="self.size >= oldSelf.size",message="size can only grow: Proxmox does not shrink a volume"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)",message="ConnectionRef is required once set"
//
//nolint:lll // CEL validation rules are too long
type VirtualMachineVolumeSpec struct {
	// NodeName is the Proxmox node the volume is created on. A VirtualMachine
	// can only attach disks that live on its own node.
	// +kubebuilder:validation:MinLength=1
	NodeName string `json:"nodeName"`
	// Storage is the Proxmox storage the volume is allocated on, as it
	// appears in `pvesm status`.
	// +kubebuilder:validation:MinLength=1
	Storage string `json:"storage"`
	// Size is the size of the disk in GiB. It can be raised later, which
	// grows the volume in place; it cannot be lowered.
	// +kubebuilder:validation:Minimum=1
	Size int `json:"size"`
	// Source populates the disk when it is created. Leave it empty for a
	// blank disk.
	// +optional
	Source *VirtualMachineVolumeSource `json:"source,omitempty"`
	// DeletionProtection keeps the volume on the holder when this object is
	// deleted, so the data survives the object. Defaults to false: deleting
	// the object frees the volume.
	// +kubebuilder:default:=false
	DeletionProtection bool `json:"deletionProtection,omitempty"`
	// +kubebuilder:validation:Required
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

// VirtualMachineVolumeSource says where the data of a new disk comes from.
// Exactly one of the fields is set.
//
// +kubebuilder:validation:XValidation:rule="has(self.volume) != has(self.disk)",message="exactly one of volume or disk must be set"
//
//nolint:lll // CEL validation rules are too long
type VirtualMachineVolumeSource struct {
	// Volume is a Proxmox volume id to copy from, such as
	// "local:import/debian-12.qcow2" (a file on a storage with the import
	// content type) or "main-pool:base-9001-disk-0" (the disk of a template).
	// The copy is made by Proxmox with import-from, and the new disk is
	// grown to Size afterwards when the source is smaller.
	// +optional
	Volume string `json:"volume,omitempty"`
	// Disk is another VirtualMachineVolume in the same namespace to copy from.
	// It has to be Ready, on the same node, and not attached to a running
	// VirtualMachine while the copy is made.
	// +optional
	Disk *corev1.LocalObjectReference `json:"disk,omitempty"`
}

// VirtualMachineVolumeHolder identifies the holder VM that keeps the volume
// while no VirtualMachine uses it.
type VirtualMachineVolumeHolder struct {
	// Name is the Proxmox VM name of the holder.
	Name string `json:"name"`
	// VMID is the Proxmox VMID of the holder.
	VMID int `json:"vmid"`
}

// VirtualMachineVolumeStatus defines the observed state of VirtualMachineVolume.
type VirtualMachineVolumeStatus struct {
	// Conditions is the metav1.Condition list of the VirtualMachineVolume.
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
	// Phase is a one-word summary of where the disk is: Pending while the
	// volume is being created or populated, Ready when it sits on the holder,
	// Attached when a VirtualMachine has it, Failed when Proxmox refused.
	// +optional
	Phase string `json:"phase,omitempty"`
	// Holder is the VM that keeps the volume while no VirtualMachine uses it.
	// +optional
	Holder *VirtualMachineVolumeHolder `json:"holder,omitempty"`
	// Slot is the device the volume is attached as, such as "scsi3": on the
	// holder while the disk is free, on the VirtualMachine named in
	// AttachedTo otherwise.
	// +optional
	Slot string `json:"slot,omitempty"`
	// Volume is the current Proxmox volume id. It changes whenever the disk
	// moves between VMs, because Proxmox renames a volume after its owner.
	// +optional
	Volume string `json:"volume,omitempty"`
	// Size is the size of the volume in GiB as Proxmox reports it.
	// +optional
	Size int `json:"size,omitempty"`
	// AttachedTo is the VirtualMachine in this namespace that currently has
	// the disk. Empty while the disk sits on the holder.
	// +optional
	AttachedTo string `json:"attachedTo,omitempty"`
	// LastObserved is when the volume was last seen on Proxmox.
	// +optional
	LastObserved *metav1.Time `json:"lastObserved,omitempty"`
	// ObservedGeneration is the .metadata.generation that the controller last
	// reconciled successfully.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

const (
	// VirtualMachineVolumePhasePending is set while the volume is created or populated.
	VirtualMachineVolumePhasePending = "Pending"
	// VirtualMachineVolumePhaseReady is set while the volume sits on the holder.
	VirtualMachineVolumePhaseReady = "Ready"
	// VirtualMachineVolumePhaseAttached is set while a VirtualMachine has the volume.
	VirtualMachineVolumePhaseAttached = "Attached"
	// VirtualMachineVolumePhaseFailed is set when Proxmox refused an operation.
	VirtualMachineVolumePhaseFailed = "Failed"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="vmvol"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="The Proxmox node"
// +kubebuilder:printcolumn:name="Storage",type="string",JSONPath=".spec.storage",description="The Proxmox storage"
// +kubebuilder:printcolumn:name="Size",type="integer",JSONPath=".spec.size",description="The size in GiB"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase",description="Where the disk is"
// +kubebuilder:printcolumn:name="Volume",type="string",JSONPath=".status.volume",description="The current Proxmox volume id"
// +kubebuilder:printcolumn:name="Attached To",type="string",JSONPath=".status.attachedTo",description="The VirtualMachine using the disk"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VirtualMachineVolume is the Schema for the virtualmachinevolumes API
type VirtualMachineVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineVolumeSpec   `json:"spec,omitempty"`
	Status VirtualMachineVolumeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualMachineVolumeList contains a list of VirtualMachineVolume
type VirtualMachineVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineVolume `json:"items"`
}

// GetConditions returns the status conditions of the VirtualMachineVolume.
func (d *VirtualMachineVolume) GetConditions() []metav1.Condition {
	return d.Status.Conditions
}

// SetConditions sets the status conditions of the VirtualMachineVolume.
func (d *VirtualMachineVolume) SetConditions(conditions []metav1.Condition) {
	d.Status.Conditions = conditions
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachineVolume{}, &VirtualMachineVolumeList{})
}
