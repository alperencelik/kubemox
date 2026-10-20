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

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// VirtualMachineVolumeReconciler reconciles a VirtualMachineVolume object.
//
// A volume is created on the holder of its namespace and node, populated
// from its source, grown when the spec asks for more, and freed when the
// object goes away. Attaching it to a VirtualMachine is the VirtualMachine
// controller's job; this one only refuses to free a volume that is attached.
type VirtualMachineVolumeReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

const (
	virtualMachineVolumeFinalizerName = "virtualmachinevolume.proxmox.alperen.cloud/finalizer"

	typeReadyVirtualMachineVolume = "Ready"

	reasonVolumeReady         = "VolumeReady"
	reasonVolumeAttached      = "VolumeAttached"
	reasonVolumeCreating      = "Creating"
	reasonVolumeSourceWaiting = "SourceNotReady"
	reasonVolumeSourceBusy    = "SourceBusy"
	reasonVolumeSourceKept    = "SourceNotCloneable"
	reasonVolumeFailed        = "ProxmoxRefused"
	reasonVolumeInUse         = "InUse"

	// volumeObservePeriod is how often a volume that sits on its holder is
	// re-read from Proxmox, so that a volume removed by hand shows up.
	volumeObservePeriod = 5 * time.Minute
	// volumeSourceRetry is how soon a volume whose source is not ready yet
	// looks again.
	volumeSourceRetry = 15 * time.Second
	// volumeInUseRetry is how soon a deleted-but-attached volume is checked
	// again for the VirtualMachine letting go of it.
	volumeInUseRetry = 30 * time.Second

	volumeMaxConcurrentReconciles = 3
)

// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinevolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinevolumes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=proxmox.alperen.cloud,resources=virtualmachinevolumes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves a VirtualMachineVolume towards its spec.
func (r *VirtualMachineVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	vol := &proxmoxv1alpha1.VirtualMachineVolume{}
	if err := r.Get(ctx, req.NamespacedName, vol); err != nil {
		if kerrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	pc, err := proxmox.NewProxmoxClientFromRef(ctx, r.Client, vol.Spec.ConnectionRef)
	if err != nil {
		logger.Error(err, "Error getting Proxmox client reference")
		return ctrl.Result{}, err
	}

	switch kubernetes.GetReconcileMode(vol) {
	case kubernetes.ReconcileModeDisable:
		logger.Info("Reconciliation is disabled for VirtualMachineVolume", "name", vol.Name)
		return ctrl.Result{}, nil
	case kubernetes.ReconcileModeWatchOnly:
		return r.observe(ctx, pc, vol)
	default:
	}

	if !vol.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(vol, virtualMachineVolumeFinalizerName) {
			return ctrl.Result{}, nil
		}
		return r.handleDelete(ctx, pc, vol)
	}
	if !controllerutil.ContainsFinalizer(vol, virtualMachineVolumeFinalizerName) {
		controllerutil.AddFinalizer(vol, virtualMachineVolumeFinalizerName)
		if err = r.Update(ctx, vol); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	if vol.Status.Slot == "" {
		return r.create(ctx, pc, vol)
	}
	if vol.Status.AttachedTo != "" {
		// The VirtualMachine controller owns the slot while the disk is
		// attached, and reports the volume id it sees there.
		return r.setReady(ctx, vol, metav1.ConditionTrue, reasonVolumeAttached,
			fmt.Sprintf("attached to VirtualMachine %s as %s", vol.Status.AttachedTo, vol.Status.Slot),
			proxmoxv1alpha1.VirtualMachineVolumePhaseAttached, volumeObservePeriod)
	}
	return r.observe(ctx, pc, vol)
}

// create allocates the volume on the holder. It blocks for the duration of
// an import, which is why this controller runs several reconciles at once.
func (r *VirtualMachineVolumeReconciler) create(ctx context.Context, pc *proxmox.ProxmoxClient,
	vol *proxmoxv1alpha1.VirtualMachineVolume) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	importFrom, result, err := r.resolveSource(ctx, vol)
	if err != nil || result != nil {
		return orEmpty(result), err
	}

	if _, err = r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeCreating,
		"creating the volume on the holder", proxmoxv1alpha1.VirtualMachineVolumePhasePending, 0); err != nil {
		return ctrl.Result{}, err
	}

	holder, err := pc.EnsureDiskHolder(ctx, vol.Namespace, vol.Spec.NodeName)
	if err != nil {
		return r.fail(ctx, vol, fmt.Errorf("ensuring the disk holder: %w", err))
	}
	slot, volid, size, err := pc.CreateHolderDisk(ctx, holder, vol.Spec.Storage, vol.Spec.Size, importFrom)
	if err != nil {
		return r.fail(ctx, vol, err)
	}
	logger.Info("Created volume", "slot", slot, "volume", volid, "sizeGiB", size, "holder", holder.VMID)
	if r.Recorder != nil {
		r.Recorder.Eventf(vol, nil, "Normal", "Created", "Created",
			"volume %s (%d GiB) created on holder %s as %s", volid, size, holder.Name, slot)
	}

	patch := client.MergeFrom(vol.DeepCopy())
	now := metav1.Now()
	vol.Status.Holder = &proxmoxv1alpha1.VirtualMachineVolumeHolder{Name: holder.Name, VMID: holder.VMID}
	vol.Status.Slot = slot
	vol.Status.Volume = volid
	vol.Status.Size = size
	vol.Status.AttachedTo = ""
	vol.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseReady
	vol.Status.LastObserved = &now
	vol.Status.ObservedGeneration = vol.Generation
	meta.SetStatusCondition(&vol.Status.Conditions, metav1.Condition{
		Type:    typeReadyVirtualMachineVolume,
		Status:  metav1.ConditionTrue,
		Reason:  reasonVolumeReady,
		Message: fmt.Sprintf("volume %s sits on holder %s as %s", volid, holder.Name, slot),
	})
	if err = r.Status().Patch(ctx, vol, patch); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: volumeObservePeriod}, nil
}

// resolveSource turns the spec source into the import-from argument: a
// volume id as given, or the current volume id of another
// VirtualMachineVolume once that one is Ready and not attached to anything.
// A source in another namespace is copied only when it is marked cloneable;
// until then this waits rather than fails, so marking the source later is
// enough.
func (r *VirtualMachineVolumeReconciler) resolveSource(ctx context.Context,
	vol *proxmoxv1alpha1.VirtualMachineVolume) (importFrom string, result *ctrl.Result, err error) {
	src := vol.Spec.Source
	switch {
	case src == nil:
		return "", nil, nil
	case src.Volume != "":
		return src.Volume, nil, nil
	case src.Disk != nil:
		key := types.NamespacedName{Namespace: src.Disk.Namespace, Name: src.Disk.Name}
		if key.Namespace == "" {
			key.Namespace = vol.Namespace
		}
		name := src.Disk.Name
		if key.Namespace != vol.Namespace {
			name = key.String()
		}
		other := &proxmoxv1alpha1.VirtualMachineVolume{}
		if err = r.Get(ctx, key, other); err != nil {
			if kerrors.IsNotFound(err) {
				res, serr := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeSourceWaiting,
					fmt.Sprintf("source VirtualMachineVolume %s does not exist", name),
					proxmoxv1alpha1.VirtualMachineVolumePhasePending, volumeSourceRetry)
				return "", &res, serr
			}
			return "", nil, err
		}
		switch {
		case key.Namespace != vol.Namespace && !other.Spec.Cloneable:
			res, serr := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeSourceKept,
				fmt.Sprintf("source VirtualMachineVolume %s is in another namespace and not cloneable; "+
					"its owner has to set spec.cloneable to true", name),
				proxmoxv1alpha1.VirtualMachineVolumePhasePending, volumeSourceRetry)
			return "", &res, serr
		case other.Status.Volume == "" || other.Status.Phase == proxmoxv1alpha1.VirtualMachineVolumePhasePending:
			res, serr := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeSourceWaiting,
				fmt.Sprintf("source VirtualMachineVolume %s is not ready yet", name),
				proxmoxv1alpha1.VirtualMachineVolumePhasePending, volumeSourceRetry)
			return "", &res, serr
		case other.Spec.NodeName != vol.Spec.NodeName:
			return "", nil, r.terminal(ctx, vol, fmt.Sprintf(
				"source VirtualMachineVolume %s lives on node %s, this one on %s; a copy cannot cross nodes",
				name, other.Spec.NodeName, vol.Spec.NodeName))
		case other.Status.AttachedTo != "":
			res, serr := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeSourceBusy,
				fmt.Sprintf("source VirtualMachineVolume %s is attached to VirtualMachine %s; "+
					"a copy is only made while the source is free", name, other.Status.AttachedTo),
				proxmoxv1alpha1.VirtualMachineVolumePhasePending, volumeSourceRetry)
			return "", &res, serr
		}
		return other.Status.Volume, nil, nil
	}
	return "", nil, r.terminal(ctx, vol, "source has neither volume nor disk")
}

// observe re-reads the volume from its holder, grows it when the spec asks
// for more, and records what Proxmox reports.
func (r *VirtualMachineVolumeReconciler) observe(ctx context.Context, pc *proxmox.ProxmoxClient,
	vol *proxmoxv1alpha1.VirtualMachineVolume) (ctrl.Result, error) {
	if vol.Status.Holder == nil || vol.Status.Slot == "" || vol.Status.AttachedTo != "" {
		return ctrl.Result{RequeueAfter: volumeObservePeriod}, nil
	}
	volid, size, err := pc.ReadDisk(ctx, vol.Spec.NodeName, vol.Status.Holder.VMID, vol.Status.Slot)
	if err != nil {
		return r.fail(ctx, vol, fmt.Errorf("reading the volume from its holder: %w", err))
	}
	if volid == "" {
		return r.fail(ctx, vol, fmt.Errorf("slot %s of holder %s (VMID %d) is empty; the volume is gone",
			vol.Status.Slot, vol.Status.Holder.Name, vol.Status.Holder.VMID))
	}
	if kubernetes.GetReconcileMode(vol) != kubernetes.ReconcileModeWatchOnly && vol.Spec.Size > size {
		if err = pc.ResizeDisk(ctx, vol.Spec.NodeName, vol.Status.Holder.VMID, vol.Status.Slot, vol.Spec.Size); err != nil {
			return r.fail(ctx, vol, err)
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(vol, nil, "Normal", "Resized", "Resized", "volume %s grown from %d to %d GiB", volid, size, vol.Spec.Size)
		}
		size = vol.Spec.Size
	}

	patch := client.MergeFrom(vol.DeepCopy())
	now := metav1.Now()
	vol.Status.Volume = volid
	vol.Status.Size = size
	vol.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseReady
	vol.Status.LastObserved = &now
	vol.Status.ObservedGeneration = vol.Generation
	meta.SetStatusCondition(&vol.Status.Conditions, metav1.Condition{
		Type:    typeReadyVirtualMachineVolume,
		Status:  metav1.ConditionTrue,
		Reason:  reasonVolumeReady,
		Message: fmt.Sprintf("volume %s sits on holder %s as %s", volid, vol.Status.Holder.Name, vol.Status.Slot),
	})
	if err = r.Status().Patch(ctx, vol, patch); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: volumeObservePeriod}, nil
}

// handleDelete frees the volume, unless a VirtualMachine still has it or
// the object asks for the data to outlive it.
func (r *VirtualMachineVolumeReconciler) handleDelete(ctx context.Context, pc *proxmox.ProxmoxClient,
	vol *proxmoxv1alpha1.VirtualMachineVolume) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	if vol.Status.AttachedTo != "" {
		msg := fmt.Sprintf("volume is attached to VirtualMachine %s; it is freed once that VirtualMachine lets go of it",
			vol.Status.AttachedTo)
		logger.Info("Volume still attached, not freeing it", "attachedTo", vol.Status.AttachedTo)
		return r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeInUse, msg,
			vol.Status.Phase, volumeInUseRetry)
	}
	switch {
	case vol.Spec.DeletionProtection:
		logger.Info("Deletion protection is on, leaving the volume on the holder",
			"volume", vol.Status.Volume, "holder", vol.Status.Holder)
	case vol.Status.Holder != nil && vol.Status.Slot != "":
		holder := &proxmox.DiskHolder{Name: vol.Status.Holder.Name, VMID: vol.Status.Holder.VMID, Node: vol.Spec.NodeName}
		if err := pc.DeleteHolderDisk(ctx, holder, vol.Status.Slot); err != nil {
			var notFound *proxmox.NotFoundError
			if !errors.As(err, &notFound) {
				return r.fail(ctx, vol, fmt.Errorf("freeing the volume: %w", err))
			}
		}
		logger.Info("Freed volume", "volume", vol.Status.Volume, "slot", vol.Status.Slot, "holder", holder.VMID)
	}
	controllerutil.RemoveFinalizer(vol, virtualMachineVolumeFinalizerName)
	if err := r.Update(ctx, vol); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// setReady writes the Ready condition and phase and returns the requeue the
// caller asked for.
func (r *VirtualMachineVolumeReconciler) setReady(ctx context.Context, vol *proxmoxv1alpha1.VirtualMachineVolume,
	status metav1.ConditionStatus, reason, message, phase string, requeue time.Duration) (ctrl.Result, error) {
	existing := meta.FindStatusCondition(vol.Status.Conditions, typeReadyVirtualMachineVolume)
	if existing == nil || existing.Status != status || existing.Reason != reason ||
		existing.Message != message || vol.Status.Phase != phase {
		patch := client.MergeFrom(vol.DeepCopy())
		vol.Status.Phase = phase
		meta.SetStatusCondition(&vol.Status.Conditions, metav1.Condition{
			Type: typeReadyVirtualMachineVolume, Status: status, Reason: reason, Message: message,
		})
		if err := r.Status().Patch(ctx, vol, patch); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// fail records a Proxmox refusal on the object and returns the error so the
// reconcile backs off; a TaskError carries Proxmox's own exit status.
func (r *VirtualMachineVolumeReconciler) fail(ctx context.Context, vol *proxmoxv1alpha1.VirtualMachineVolume,
	err error) (ctrl.Result, error) {
	log.FromContext(ctx).Error(err, "VirtualMachineVolume operation failed")
	if r.Recorder != nil {
		r.Recorder.Eventf(vol, nil, "Warning", reasonVolumeFailed, "Failed", "%s", err.Error())
	}
	if _, serr := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeFailed, err.Error(),
		proxmoxv1alpha1.VirtualMachineVolumePhaseFailed, 0); serr != nil {
		return ctrl.Result{}, serr
	}
	return ctrl.Result{}, err
}

// terminal records a spec problem that no retry fixes and stops without an
// error, so the object is not requeued until it changes.
func (r *VirtualMachineVolumeReconciler) terminal(ctx context.Context, vol *proxmoxv1alpha1.VirtualMachineVolume,
	message string) error {
	if r.Recorder != nil {
		r.Recorder.Eventf(vol, nil, "Warning", reasonVolumeFailed, "Invalid", "%s", message)
	}
	_, err := r.setReady(ctx, vol, metav1.ConditionFalse, reasonVolumeFailed, message,
		proxmoxv1alpha1.VirtualMachineVolumePhaseFailed, 0)
	return err
}

func orEmpty(r *ctrl.Result) ctrl.Result {
	if r == nil {
		return ctrl.Result{}
	}
	return *r
}

// SetupWithManager sets up the controller with the Manager.
func (r *VirtualMachineVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxmoxv1alpha1.VirtualMachineVolume{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: volumeMaxConcurrentReconciles}).
		Complete(r)
}
