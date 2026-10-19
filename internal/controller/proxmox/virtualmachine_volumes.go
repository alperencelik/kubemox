package proxmox

import (
	"context"
	"errors"
	"fmt"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// A VirtualMachine attaches VirtualMachineVolumes by volumeRef. The volume
// is reassigned from its holder onto the VM's device when the VM exists,
// and given back to the holder when the ref is removed or the VM is deleted,
// before the VM is destroyed: Proxmox destroys every drive still attached.
// The volume's status is written here while it is attached, so the volume
// controller leaves it alone until it is back on the holder.

const (
	// volumeAttachRetry is how soon a VM looks again at a volume that is not
	// ready to be attached yet.
	volumeAttachRetry = 15 * time.Second
	// volumeBusyRetry is how soon a VM looks again at a volume another VM
	// holds, or at a detach that waits for the VM to stop.
	volumeBusyRetry = time.Minute

	reasonVolumeAttachedToVM = "VolumeAttached"
	reasonVolumeDetached     = "VolumeDetached"
	reasonVolumeNotReady     = "VolumeNotReady"
	reasonVolumeHeldByOther  = "VolumeHeldByOtherVM"
	reasonVolumeWrongNode    = "VolumeOnOtherNode"
	reasonVolumeLost         = "VolumeLost"
	reasonDetachWaitsForStop = "DetachWaitsForStop"
)

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinevolumes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinevolumes/status,verbs=get;update;patch

// wantedVolumes maps the volumes the spec attaches to the device each one
// goes to.
func wantedVolumes(vm *proxmoxv1alpha1.VirtualMachine) map[string]string {
	wanted := map[string]string{}
	for _, ref := range proxmox.VolumeRefs(vm) {
		wanted[ref.VolumeRef.Name] = ref.Device
	}
	return wanted
}

// staleVolumes picks, among the volumes attached to the VM, those the spec
// no longer names, or names at another device.
func staleVolumes(vm *proxmoxv1alpha1.VirtualMachine,
	attached []proxmoxv1alpha1.VirtualMachineVolume) []proxmoxv1alpha1.VirtualMachineVolume {
	wanted := wantedVolumes(vm)
	stale := []proxmoxv1alpha1.VirtualMachineVolume{}
	for _, vol := range attached {
		if vol.Status.AttachedTo != vm.Name {
			continue
		}
		if device, ok := wanted[vol.Name]; !ok || device != vol.Status.Slot {
			stale = append(stale, vol)
		}
	}
	return stale
}

// reconcileVolumes attaches every volumeRef drive that is not attached yet
// and gives back the volumes the spec no longer names. A non-nil result asks
// the caller to stop and requeue; the VM is not started meanwhile.
func (r *VirtualMachineReconciler) reconcileVolumes(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	for _, ref := range proxmox.VolumeRefs(vm) {
		vol := &proxmoxv1alpha1.VirtualMachineVolume{}
		key := types.NamespacedName{Namespace: vm.Namespace, Name: ref.VolumeRef.Name}
		if err := r.Get(ctx, key, vol); err != nil {
			if kerrors.IsNotFound(err) {
				r.volumeEvent(vm, "Warning", reasonVolumeNotReady,
					"VirtualMachineVolume %s does not exist; %s stays empty until it does", ref.VolumeRef.Name, ref.Device)
				return &ctrl.Result{RequeueAfter: volumeAttachRetry}, nil
			}
			return nil, err
		}
		switch {
		case vol.Status.AttachedTo == vm.Name && vol.Status.Slot == ref.Device:
			continue
		case vol.Spec.NodeName != vm.Spec.NodeName:
			r.volumeEvent(vm, "Warning", reasonVolumeWrongNode,
				"VirtualMachineVolume %s lives on node %s, this VirtualMachine on %s; a volume cannot cross nodes",
				vol.Name, vol.Spec.NodeName, vm.Spec.NodeName)
			return &ctrl.Result{RequeueAfter: volumeBusyRetry}, nil
		case vol.Status.AttachedTo != "" && vol.Status.AttachedTo != vm.Name:
			r.volumeEvent(vm, "Warning", reasonVolumeHeldByOther,
				"VirtualMachineVolume %s is attached to VirtualMachine %s", vol.Name, vol.Status.AttachedTo)
			return &ctrl.Result{RequeueAfter: volumeBusyRetry}, nil
		case vol.Status.AttachedTo == vm.Name:
			// Attached to this VM at another device: the spec moved it. Give
			// it back first; the stale pass below does that, and the next
			// reconcile attaches it where the spec wants it now.
			continue
		case vol.Status.Phase != proxmoxv1alpha1.VirtualMachineVolumePhaseReady || vol.Status.Holder == nil:
			r.volumeEvent(vm, "Normal", reasonVolumeNotReady,
				"VirtualMachineVolume %s is %s; waiting for it to be Ready", vol.Name, vol.Status.Phase)
			return &ctrl.Result{RequeueAfter: volumeAttachRetry}, nil
		}

		volid, err := pc.AttachVolume(ctx, vm, ref.Device, vol)
		if err != nil {
			r.volumeEvent(vm, "Warning", "Error", "attaching VirtualMachineVolume %s at %s: %v", vol.Name, ref.Device, err)
			var taskErr *proxmox.TaskError
			if errors.As(err, &taskErr) {
				return &dontRequeue, reconcile.TerminalError(err)
			}
			return nil, err
		}
		if err = r.patchVolumeStatus(ctx, vol, func(v *proxmoxv1alpha1.VirtualMachineVolume) {
			v.Status.AttachedTo = vm.Name
			v.Status.Slot = ref.Device
			v.Status.Volume = volid
			v.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseAttached
			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type: typeReadyVirtualMachineVolume, Status: metav1.ConditionTrue, Reason: reasonVolumeAttachedToVM,
				Message: fmt.Sprintf("attached to VirtualMachine %s as %s", vm.Name, ref.Device),
			})
		}); err != nil {
			return nil, err
		}
		logger.Info("Attached volume", "volume", vol.Name, "device", ref.Device, "volid", volid)
		r.volumeEvent(vm, "Normal", reasonVolumeAttachedToVM, "VirtualMachineVolume %s attached as %s (%s)", vol.Name, ref.Device, volid)
	}

	attached, err := r.attachedVolumes(ctx, vm)
	if err != nil {
		return nil, err
	}
	stale := staleVolumes(vm, attached)
	if len(stale) == 0 {
		return nil, nil
	}
	state, err := pc.GetVMState(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		return nil, err
	}
	if state == proxmox.VirtualMachineRunningState {
		// A drive in use cannot be reassigned, and stopping a machine the
		// spec still wants running is not this controller's call.
		names := make([]string, 0, len(stale))
		for _, v := range stale {
			names = append(names, v.Name)
		}
		r.volumeEvent(vm, "Warning", reasonDetachWaitsForStop,
			"VirtualMachineVolume %v no longer in the spec; it is given back once the VirtualMachine is stopped", names)
		return &ctrl.Result{RequeueAfter: volumeBusyRetry}, nil
	}
	for i := range stale {
		if err := r.detachVolume(ctx, pc, vm, &stale[i]); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// releaseVolumes gives every attached volume back to its holder so that
// destroying the VM does not take it along. Deletion implies the VM stops,
// so a running one is stopped here first.
func (r *VirtualMachineReconciler) releaseVolumes(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine) error {
	attached, err := r.attachedVolumes(ctx, vm)
	if err != nil {
		return err
	}
	mine := make([]proxmoxv1alpha1.VirtualMachineVolume, 0, len(attached))
	for _, vol := range attached {
		if vol.Status.AttachedTo == vm.Name {
			mine = append(mine, vol)
		}
	}
	if len(mine) == 0 {
		return nil
	}
	state, err := pc.GetVMState(vm.Spec.Name, vm.Spec.NodeName)
	if err != nil {
		var notFound *proxmox.NotFoundError
		if errors.As(err, &notFound) {
			return r.markVolumesLost(ctx, vm, mine)
		}
		return err
	}
	if state == proxmox.VirtualMachineRunningState {
		if err = pc.StopVM(vm.Spec.Name, vm.Spec.NodeName); err != nil {
			return fmt.Errorf("stopping VirtualMachine %s before giving its volumes back: %w", vm.Spec.Name, err)
		}
	}
	for i := range mine {
		if err := r.detachVolume(ctx, pc, vm, &mine[i]); err != nil {
			return err
		}
	}
	return nil
}

// detachVolume moves one volume back to its holder and records where it is.
func (r *VirtualMachineReconciler) detachVolume(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine, vol *proxmoxv1alpha1.VirtualMachineVolume) error {
	holder, slot, volid, err := pc.DetachVolume(ctx, vm, vol.Status.Slot, vol)
	if err != nil {
		var notFound *proxmox.NotFoundError
		if errors.As(err, &notFound) {
			// The VM, or the drive on it, is gone: so is the data.
			return r.markVolumesLost(ctx, vm, []proxmoxv1alpha1.VirtualMachineVolume{*vol})
		}
		r.volumeEvent(vm, "Warning", "Error", "giving back VirtualMachineVolume %s: %v", vol.Name, err)
		return err
	}
	if err = r.patchVolumeStatus(ctx, vol, func(v *proxmoxv1alpha1.VirtualMachineVolume) {
		v.Status.AttachedTo = ""
		v.Status.Holder = &proxmoxv1alpha1.VirtualMachineVolumeHolder{Name: holder.Name, VMID: holder.VMID}
		v.Status.Slot = slot
		v.Status.Volume = volid
		v.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseReady
		meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
			Type: typeReadyVirtualMachineVolume, Status: metav1.ConditionTrue, Reason: reasonVolumeReady,
			Message: fmt.Sprintf("volume %s sits on holder %s as %s", volid, holder.Name, slot),
		})
	}); err != nil {
		return err
	}
	log.FromContext(ctx).Info("Gave volume back to its holder", "volume", vol.Name, "slot", slot, "volid", volid)
	r.volumeEvent(vm, "Normal", reasonVolumeDetached, "VirtualMachineVolume %s given back to holder %s as %s", vol.Name, holder.Name, slot)
	return nil
}

// markVolumesLost records that the drive a volume was attached to no longer
// exists on Proxmox, so the volume controller does not go looking for it on
// the holder.
func (r *VirtualMachineReconciler) markVolumesLost(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	vols []proxmoxv1alpha1.VirtualMachineVolume) error {
	for i := range vols {
		vol := &vols[i]
		msg := fmt.Sprintf("VirtualMachine %s, which held the volume as %s, is gone from Proxmox and so is the volume",
			vm.Spec.Name, vol.Status.Slot)
		if err := r.patchVolumeStatus(ctx, vol, func(v *proxmoxv1alpha1.VirtualMachineVolume) {
			v.Status.AttachedTo = ""
			v.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseFailed
			meta.SetStatusCondition(&v.Status.Conditions, metav1.Condition{
				Type: typeReadyVirtualMachineVolume, Status: metav1.ConditionFalse, Reason: reasonVolumeLost, Message: msg,
			})
		}); err != nil {
			return err
		}
		r.volumeEvent(vm, "Warning", reasonVolumeLost, "%s", msg)
	}
	return nil
}

// attachedVolumes lists the volumes of the namespace that record this VM as
// their holder-of-the-moment.
func (r *VirtualMachineReconciler) attachedVolumes(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) ([]proxmoxv1alpha1.VirtualMachineVolume, error) {
	var list proxmoxv1alpha1.VirtualMachineVolumeList
	if err := r.List(ctx, &list, client.InNamespace(vm.Namespace)); err != nil {
		return nil, fmt.Errorf("listing VirtualMachineVolumes in %s: %w", vm.Namespace, err)
	}
	attached := make([]proxmoxv1alpha1.VirtualMachineVolume, 0)
	for _, vol := range list.Items {
		if vol.Status.AttachedTo == vm.Name {
			attached = append(attached, vol)
		}
	}
	return attached, nil
}

func (r *VirtualMachineReconciler) patchVolumeStatus(ctx context.Context, vol *proxmoxv1alpha1.VirtualMachineVolume,
	mutate func(*proxmoxv1alpha1.VirtualMachineVolume)) error {
	patch := client.MergeFrom(vol.DeepCopy())
	mutate(vol)
	now := metav1.Now()
	vol.Status.LastObserved = &now
	if err := r.Status().Patch(ctx, vol, patch); err != nil {
		return fmt.Errorf("updating VirtualMachineVolume %s status: %w", vol.Name, err)
	}
	return nil
}

func (r *VirtualMachineReconciler) volumeEvent(vm *proxmoxv1alpha1.VirtualMachine, kind, reason, format string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(vm, nil, kind, reason, reason, format, args...)
}

// vmsForVolume maps a VirtualMachineVolume event to the VirtualMachines of
// its namespace that reference it, so a volume turning Ready, or coming
// back to its holder, wakes the VM waiting for it.
func (r *VirtualMachineReconciler) vmsForVolume(ctx context.Context, obj client.Object) []reconcile.Request {
	vol, ok := obj.(*proxmoxv1alpha1.VirtualMachineVolume)
	if !ok {
		return nil
	}
	var vms proxmoxv1alpha1.VirtualMachineList
	if err := r.List(ctx, &vms, client.InNamespace(vol.Namespace)); err != nil {
		return nil
	}
	requests := []reconcile.Request{}
	for i := range vms.Items {
		vm := &vms.Items[i]
		if _, wants := wantedVolumes(vm)[vol.Name]; wants || vol.Status.AttachedTo == vm.Name {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(vm)})
		}
	}
	return requests
}
