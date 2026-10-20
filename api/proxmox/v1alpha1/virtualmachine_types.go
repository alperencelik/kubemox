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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// VirtualMachineSpec defines the desired state of VirtualMachine

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.connectionRef) || has(self.connectionRef)", message="ConnectionRef is required once set"
// +kubebuilder:validation:XValidation:rule="has(self.template) || has(self.vmSpec)",message="One of template or vmSpec must be specified"
// +kubebuilder:validation:XValidation:rule="!has(self.template) || !has(self.vmSpec)",message="template and vmSpec are mutually exclusive"
//
//nolint:lll // CEL validation rules are too long
type VirtualMachineSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Name is the name of the VM
	Name string `json:"name"`
	// NodeName is the name of the target node of Proxmox
	NodeName string `json:"nodeName"`
	// TemplateSpec of the source VM
	Template *VirtualMachineSpecTemplate `json:"template,omitempty"`
	// This field should be modified further
	VMSpec *NewVMSpec `json:"vmSpec,omitempty"`
	// DeletionProtection is a flag that indicates whether the VM should be protected from deletion.
	// If true, the VM will not be deleted when the Kubernetes resource is deleted.
	// If not set, it defaults to false.
	// +kubebuilder:default:=false
	DeletionProtection bool `json:"deletionProtection,omitempty"`
	// EnableAutoStart is a flag that indicates whether the VM should automatically start when it's powered off.
	// If true, the VM will start automatically when it's powered off.
	// If not set, it defaults to true. PowerState, when set, takes precedence.
	// +kubebuilder:default:=true
	EnableAutoStart bool `json:"enableAutoStart,omitempty"`
	// PowerState is what the operator does about the machine's power.
	// Running starts it whenever it is found stopped, Stopped stops it whenever
	// it is found running, Manual leaves the power alone and only keeps the
	// configuration. Unset keeps the EnableAutoStart behaviour.
	// +kubebuilder:validation:Enum=Running;Stopped;Manual
	// +optional
	PowerState PowerState `json:"powerState,omitempty"`
	// Pool is the Proxmox resource pool the machine is created in. It is
	// applied at creation; a change afterwards is not applied.
	// +optional
	Pool string `json:"pool,omitempty"`
	// Tags are Proxmox tags kept on the machine next to the operator's own
	// tag. Tags present on the machine but not listed here are left alone.
	// +optional
	Tags []string `json:"tags,omitempty"`
	// AdditionalConfig is the additional configuration of the VM
	// +kubebuilder:validation:Optional
	AdditionalConfig map[string]string `json:"additionalConfig,omitempty"`
	// CloneMode selects how the template's disks are cloned. Full copies them,
	// which costs the size of the template per VM. Linked keeps a reference to
	// the template's base snapshot, which costs almost nothing at creation but
	// pins the template: it cannot be removed or rotated while a linked clone
	// still references it. Proxmox places a linked clone on the template's own
	// storage, so it cannot be moved elsewhere at creation time.
	// +kubebuilder:validation:Enum=Full;Linked
	// +kubebuilder:default:=Full
	CloneMode CloneMode `json:"cloneMode,omitempty"`

	// +kubebuilder:validation:Required
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
}

// PowerState is what the operator does about a machine's power.
type PowerState string

const (
	// PowerStateRunning starts the machine whenever it is found stopped.
	PowerStateRunning PowerState = "Running"
	// PowerStateStopped stops the machine whenever it is found running.
	PowerStateStopped PowerState = "Stopped"
	// PowerStateManual leaves the power alone and only keeps the configuration.
	PowerStateManual PowerState = "Manual"
)

// CloneMode selects between a full copy of the template's disks and a linked
// clone that references the template's base snapshot.
type CloneMode string

const (
	// CloneModeFull copies the template's disks.
	CloneModeFull CloneMode = "Full"
	// CloneModeLinked references the template's base snapshot.
	CloneModeLinked CloneMode = "Linked"
)

// IsFullClone reports whether the disks should be copied. An empty value means
// Full: that was the only behaviour before this field existed, so an object
// written against the older API keeps it.
func (c CloneMode) IsFullClone() bool {
	return c != CloneModeLinked
}

// CloneFlag returns what the Proxmox API expects for the clone "full" option.
func (c CloneMode) CloneFlag() uint8 {
	if c.IsFullClone() {
		return 1
	}
	return 0
}

// type connRef struct {
// *corev1.TypedLocalObjectReference `json:"connectionRef,omitempty"`
// }

type NewVMSpec struct {
	// Cores is the number of CPU cores
	// +kubebuilder:validation:Minimum=1
	Cores int `json:"cores,omitempty"`
	// Socket is the number of CPU sockets
	// +kubebuilder:default:=1
	// +kubebuilder:validation:Minimum=1
	Socket int `json:"socket,omitempty"`
	// Memory is the amount of memory in MB
	// +kubebuilder:validation:Minimum=16
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []VirtualMachineDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network *[]VirtualMachineNetwork `json:"network,omitempty"`
	// OS Image
	OSImage NewVMSpecOSImage `json:"osImage,omitempty"`
	// PCI is the list of PCI devices
	PciDevices []PciDevice `json:"pciDevices,omitempty"`
	// Cloud Init Config
	CloudInitConfig *CloudInitConfig `json:"cloudInitConfig,omitempty"`
	// SMBIOSUUID sets the UUID in smbios1. Cloud-init takes it as the
	// instance identity, so a new value makes it run again on the next boot.
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	// +optional
	SMBIOSUUID string `json:"smbiosUUID,omitempty"`
	// Firmware selects BIOS or UEFI, the EFI vars disk, a TPM and the machine type.
	// +optional
	Firmware *VirtualMachineFirmware `json:"firmware,omitempty"`
	// GuestAgent enables the QEMU guest agent channel (agent=1). Without it
	// Proxmox cannot ask the guest for its addresses or run commands in it,
	// and the operator's status shows no IP. A clone inherits the template's
	// setting; a machine built from scratch has it off unless set here.
	// +kubebuilder:default:=false
	// +optional
	GuestAgent bool `json:"guestAgent,omitempty"`
}

// VirtualMachineFirmware is the firmware side of a machine built from scratch.
type VirtualMachineFirmware struct {
	// Bios is seabios (the Proxmox default) or ovmf for UEFI.
	// +kubebuilder:validation:Enum=seabios;ovmf
	// +optional
	Bios string `json:"bios,omitempty"`
	// Machine is the QEMU machine type, such as q35. Empty keeps the Proxmox default (i440fx).
	// +optional
	Machine string `json:"machine,omitempty"`
	// SCSIController is the SCSI controller model (scsihw): virtio-scsi-single,
	// virtio-scsi-pci, lsi, lsi53c810, megasas or pvscsi. Empty keeps the
	// Proxmox default (lsi), which a cloud image built for virtio-scsi, such
	// as Ubuntu's, cannot boot from.
	// +kubebuilder:validation:Enum=virtio-scsi-single;virtio-scsi-pci;lsi;lsi53c810;megasas;pvscsi
	// +optional
	SCSIController string `json:"scsiController,omitempty"`
	// EFIDisk is the EFI vars disk ovmf needs. It is created with the machine
	// and left alone afterwards.
	// +optional
	EFIDisk *VirtualMachineEFIDisk `json:"efiDisk,omitempty"`
	// TPM is a TPM state disk. It is created with the machine and left alone afterwards.
	// +optional
	TPM *VirtualMachineTPM `json:"tpm,omitempty"`
}

// VirtualMachineEFIDisk is the efidisk0 of a UEFI machine.
type VirtualMachineEFIDisk struct {
	// Storage the EFI vars disk is allocated on.
	// +kubebuilder:validation:MinLength=1
	Storage string `json:"storage"`
	// PreEnrolledKeys enrolls the Microsoft Secure Boot keys, which is what a
	// Secure Boot guest expects. Off leaves Secure Boot disabled.
	// +kubebuilder:default:=false
	PreEnrolledKeys bool `json:"preEnrolledKeys,omitempty"`
}

// VirtualMachineTPM is the tpmstate0 of a machine.
type VirtualMachineTPM struct {
	// Storage the TPM state disk is allocated on.
	// +kubebuilder:validation:MinLength=1
	Storage string `json:"storage"`
	// Version is the TPM version: v2.0 (default) or v1.2.
	// +kubebuilder:validation:Enum=v1.2;v2.0
	// +kubebuilder:default:="v2.0"
	// +optional
	Version string `json:"version,omitempty"`
}

type NewVMSpecOSImage struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type VirtualMachineSpecTemplate struct {
	// Name of the template
	Name string `json:"name,omitempty"`
	// Cores is the number of CPU cores
	// +kubebuilder:validation:Minimum=1
	Cores int `json:"cores,omitempty"`
	// Socket is the number of CPU sockets
	// +kubebuilder:validation:Minimum=1
	Socket int `json:"socket,omitempty"`
	// Memory is the amount of memory in MB
	// +kubebuilder:validation:Minimum=16
	Memory int `json:"memory,omitempty"`
	// Disks is the list of disks
	Disk []VirtualMachineDisk `json:"disk,omitempty"`
	// Networks is the list of networks
	Network *[]VirtualMachineNetwork `json:"network,omitempty"`
	// PCI is the list of PCI devices
	PciDevices []PciDevice `json:"pciDevices,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.type != 'mapped' || self.pcie == false",message="pcie cannot be true when device type is mapped"
type PciDevice struct {
	// Type is the type of the PCI device either raw or mapped
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=raw;mapped
	Type string `json:"type"`
	// DeviceID is the ID hex id of the device
	DeviceID string `json:"deviceID,omitempty"`
	// PrimaryGPU is the flag that indicates whether the device is the primary GPU ==> x-vga=1
	// +kubebuilder:default:=false
	PrimaryGPU bool `json:"primaryGPU,omitempty"`
	// PCIE is the flag that indicates whether the device is a PCIE device ==> pcie=1
	// +kubebuilder:default:=false
	PCIE bool `json:"pcie,omitempty"`
}

type VirtualMachineDisk struct {
	// Storage is the name of the storage
	Storage string `json:"storage"`
	// Size is the size of the disk in GB
	// +kubebuilder:validation:Minimum=1
	Size int `json:"size"`
	// Device is the name of the device
	Device string `json:"device"`
}

type VirtualMachineNetwork struct {
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
	// IPAddress is the IP address of the VM (or LXC). Optional so status subresources can be patched incrementally.
	IPAddress string `json:"IPAddress,omitempty"`
	// OSInfo is the OS information of the VM (or LXC, often config ostype).
	OSInfo string `json:"OSInfo,omitempty"`
}

// VirtualMachineStatus defines the observed state of VirtualMachine
type VirtualMachineStatus struct {
	// Conditions is the metav1.Condition of the Virtual Machine
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"` //nolint:lll // This is required by kubebuilder
	// Status is the QEMU status of the Virtual Machine (state, node, uptime, id, IP address, os info)
	Status *QEMUStatus `json:"status,omitempty"`
	// LastObserved is the timestamp of the last successful observation of the VM
	// state from Proxmox. A stale value relative to wall-clock time indicates the
	// operator has lost the ability to verify .status.status against Proxmox.
	// +optional
	LastObserved *metav1.Time `json:"lastObserved,omitempty"`
	// ObservedGeneration is the .metadata.generation that the controller last
	// reconciled successfully.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope="Cluster",shortName="vm"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="The node name"
// +kubebuilder:printcolumn:name="ID",type="integer",JSONPath=".status.status.id",description="The ID of the VM"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.status.state",description="The state of the VM"
// +kubebuilder:printcolumn:name="Uptime",type="string",JSONPath=".status.status.uptime",description="The uptime of the VM"
// +kubebuilder:printcolumn:name="IP Address",type="string",JSONPath=".status.status.IPAddress",description="The IP address of the VM"
// +kubebuilder:printcolumn:name="OS Info",type="string",JSONPath=".status.status.OSInfo",description="The OS information of the VM"
// +kubebuilder:printcolumn:name="Last Observed",type="date",JSONPath=".status.lastObserved",description="The last time the VM state was observed from Proxmox"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// VirtualMachine is the Schema for the virtualmachines API
type VirtualMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineSpec   `json:"spec,omitempty"`
	Status VirtualMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualMachineList contains a list of VirtualMachine
type VirtualMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachine `json:"items"`
}

// GetConditions returns the status conditions of the VirtualMachine.
func (vm *VirtualMachine) GetConditions() []metav1.Condition {
	return vm.Status.Conditions
}

// SetConditions sets the status conditions of the VirtualMachine.
func (vm *VirtualMachine) SetConditions(conditions []metav1.Condition) {
	vm.Status.Conditions = conditions
}

// MarkObserved stamps LastObserved with the supplied time and records the
// generation that produced the current status. Call only after a successful
// observation against Proxmox so consumers can distinguish fresh status from
// status that the operator could not re-verify.
func (s *VirtualMachineStatus) MarkObserved(t time.Time, generation int64) {
	now := metav1.NewTime(t)
	s.LastObserved = &now
	s.ObservedGeneration = generation
}

func init() { //nolint:gochecknoinits // This is required by kubebuilder
	SchemeBuilder.Register(&VirtualMachine{}, &VirtualMachineList{})
}
