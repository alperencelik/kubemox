package proxmox

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// spec.vmSpec.cloudInit: a NoCloud datasource built from a Secret and
// attached as an ISO. The ISO is rebuilt whenever its content changes, with
// an instance id derived from the content, so the guest runs cloud-init
// again on its next boot; nothing here reboots it.

const (
	typeCloudInitReadyVirtualMachine = "CloudInitReady"
	reasonCloudInitApplied           = "CloudInitApplied"
	reasonCloudInitSecretMissing     = "SecretMissing"
	reasonCloudInitSecretInvalid     = "SecretInvalid"

	cloudInitKeyUserData      = "user-data"
	cloudInitKeyMetaData      = "meta-data"
	cloudInitKeyNetworkConfig = "network-config"
	cloudInitKeyVendorData    = "vendor-data"
	cloudInitDefaultDevice    = "ide2"
)

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// buildNoCloudData assembles the datasource from the Secret and, where the
// Secret leaves gaps, from the machine: the instance id from the content and
// the name, the network-config from the ports that got an address.
func buildNoCloudData(vm *proxmoxv1alpha1.VirtualMachine, secret *corev1.Secret) (proxmox.NoCloudData, string, error) {
	data := proxmox.NoCloudData{
		UserData:      string(secret.Data[cloudInitKeyUserData]),
		MetaData:      string(secret.Data[cloudInitKeyMetaData]),
		VendorData:    string(secret.Data[cloudInitKeyVendorData]),
		NetworkConfig: string(secret.Data[cloudInitKeyNetworkConfig]),
	}
	if strings.TrimSpace(data.UserData) == "" {
		return data, "", fmt.Errorf("the Secret %s has no %s", secret.Name, cloudInitKeyUserData)
	}
	src := vm.Spec.VMSpec.CloudInit
	if data.NetworkConfig == "" {
		ports := make([]proxmox.PortAddress, 0, len(vm.Status.Networks))
		for _, n := range vm.Status.Networks {
			ports = append(ports, proxmox.PortAddress{MACAddress: n.MACAddress, Address: n.Address, Gateway: n.Gateway})
		}
		data.NetworkConfig = proxmox.RenderNetworkConfig(ports, src.Nameservers)
	}
	instanceID := ""
	if data.MetaData == "" {
		// The id covers everything but the meta-data itself, so that a
		// change anywhere gives cloud-init a new instance to run for.
		contentHash := proxmox.NoCloudData{UserData: data.UserData, VendorData: data.VendorData, NetworkConfig: data.NetworkConfig}.Hash()
		instanceID = fmt.Sprintf("%s-%s", vm.Spec.Name, contentHash[:12])
		data.MetaData = proxmox.RenderMetaData(instanceID, vm.Spec.Name)
	} else {
		instanceID = instanceIDOf(data.MetaData)
	}
	return data, instanceID, nil
}

// instanceIDOf reads instance-id out of a meta-data document.
func instanceIDOf(metaData string) string {
	for _, line := range strings.Split(metaData, "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(key) == "instance-id" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// reconcileNoCloud builds and attaches the datasource when the spec asks
// for one and its content changed since the last apply.
func (r *VirtualMachineReconciler) reconcileNoCloud(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine) error {
	if vm.Spec.VMSpec == nil || vm.Spec.VMSpec.CloudInit == nil {
		return nil
	}
	logger := log.FromContext(ctx)
	src := vm.Spec.VMSpec.CloudInit
	device := src.Device
	if device == "" {
		device = cloudInitDefaultDevice
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: vm.Namespace, Name: src.SecretRef.Name}, secret); err != nil {
		if kerrors.IsNotFound(err) {
			msg := fmt.Sprintf("Secret %s does not exist", src.SecretRef.Name)
			r.cloudInitEvent(vm, "Warning", reasonCloudInitSecretMissing, "%s", msg)
			if cerr := r.setCloudInitCondition(ctx, vm, metav1.ConditionFalse, reasonCloudInitSecretMissing, msg); cerr != nil {
				return cerr
			}
			return fmt.Errorf("cloud-init: %s", msg)
		}
		return fmt.Errorf("reading cloud-init Secret %s: %w", src.SecretRef.Name, err)
	}
	data, instanceID, err := buildNoCloudData(vm, secret)
	if err != nil {
		r.cloudInitEvent(vm, "Warning", reasonCloudInitSecretInvalid, "%v", err)
		if cerr := r.setCloudInitCondition(ctx, vm, metav1.ConditionFalse, reasonCloudInitSecretInvalid, err.Error()); cerr != nil {
			return cerr
		}
		return fmt.Errorf("cloud-init: %w", err)
	}
	hash := data.Hash()
	if vm.Status.CloudInit != nil && vm.Status.CloudInit.Hash == hash && vm.Status.CloudInit.Device == device {
		return nil
	}

	if err = pc.ApplyNoCloud(vm, device, data); err != nil {
		r.cloudInitEvent(vm, "Warning", "Error", "%v", err)
		return err
	}
	rebuilt := vm.Status.CloudInit != nil
	patch := client.MergeFrom(vm.DeepCopy())
	vm.Status.CloudInit = &proxmoxv1alpha1.VirtualMachineCloudInitStatus{Hash: hash, InstanceID: instanceID, Device: device}
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type: typeCloudInitReadyVirtualMachine, Status: metav1.ConditionTrue, Reason: reasonCloudInitApplied,
		Message: fmt.Sprintf("NoCloud ISO from Secret %s attached at %s, instance-id %s", src.SecretRef.Name, device, instanceID),
	})
	if err = r.Status().Patch(ctx, vm, patch); err != nil {
		return client.IgnoreNotFound(err)
	}
	logger.Info("Applied NoCloud datasource", "secret", src.SecretRef.Name, "device", device, "instanceID", instanceID)
	if rebuilt {
		r.cloudInitEvent(vm, "Normal", reasonCloudInitApplied,
			"cloud-init ISO rebuilt from Secret %s with instance-id %s; the guest applies it on its next boot", src.SecretRef.Name, instanceID)
	} else {
		r.cloudInitEvent(vm, "Normal", reasonCloudInitApplied, "cloud-init ISO from Secret %s attached at %s", src.SecretRef.Name, device)
	}
	return nil
}

func (r *VirtualMachineReconciler) setCloudInitCondition(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	status metav1.ConditionStatus, reason, message string) error {
	existing := meta.FindStatusCondition(vm.Status.Conditions, typeCloudInitReadyVirtualMachine)
	if existing != nil && existing.Status == status && existing.Reason == reason && existing.Message == message {
		return nil
	}
	patch := client.MergeFrom(vm.DeepCopy())
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type: typeCloudInitReadyVirtualMachine, Status: status, Reason: reason, Message: message,
	})
	if err := r.Status().Patch(ctx, vm, patch); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func (r *VirtualMachineReconciler) cloudInitEvent(vm *proxmoxv1alpha1.VirtualMachine, kind, reason, format string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(vm, nil, kind, reason, reason, format, args...)
}
