package proxmox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/proxmox"
)

// A network port with an IPAddressClaimRef gets its address from the Cluster
// API IPAM contract (ipam.cluster.x-k8s.io): the claim is bound by an IPAM
// provider to an IPAddress that carries address, prefix and gateway. Those
// are read here as unstructured objects, so the operator takes no dependency
// on the cluster-api module and runs on a cluster without the CRDs; a ref on
// such a cluster is reported, not crashed on.

var (
	ipAddressClaimGVK = schema.GroupVersionKind{Group: "ipam.cluster.x-k8s.io", Version: "v1beta1", Kind: "IPAddressClaim"}
	ipAddressGVK      = schema.GroupVersionKind{Group: "ipam.cluster.x-k8s.io", Version: "v1beta1", Kind: "IPAddress"}
)

const (
	// ipAddressRetry is how soon a machine whose claim is not bound yet looks again.
	ipAddressRetry = 15 * time.Second

	typeNetworkReadyVirtualMachine = "NetworkReady"
	reasonWaitingForIPAddress      = "WaitingForIPAddress"
	reasonIPAddressesReady         = "IPAddressesReady"
	reasonIPAMUnavailable          = "IPAMUnavailable"
)

// +kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch

// reader is the client the claims are read with: the API reader when the
// manager gave one, so that the optional CRDs start no informer, otherwise
// the cached client.
func (r *VirtualMachineReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// resolveNetworkIPs looks every claimed port's address up. It returns the
// resolved addresses, the names of the claims still waiting, and an error
// only for a failure that is not "not bound yet".
func (r *VirtualMachineReconciler) resolveNetworkIPs(ctx context.Context,
	vm *proxmoxv1alpha1.VirtualMachine) (ips []proxmox.NetworkIP, waiting []string, err error) {
	for i, n := range networksOf(vm) {

		if n.IPAddressClaimRef == nil {
			continue
		}
		claimName := n.IPAddressClaimRef.Name
		claim := &unstructured.Unstructured{}
		claim.SetGroupVersionKind(ipAddressClaimGVK)
		getErr := r.reader().Get(ctx, types.NamespacedName{Namespace: vm.Namespace, Name: claimName}, claim)
		switch {
		case getErr == nil:
		case kerrors.IsNotFound(getErr):
			waiting = append(waiting, claimName+" (claim does not exist)")
			continue
		case meta.IsNoMatchError(getErr):
			return nil, nil, fmt.Errorf("%s: the IPAddressClaim API (ipam.cluster.x-k8s.io) is not installed on this cluster",
				reasonIPAMUnavailable)
		default:
			return nil, nil, fmt.Errorf("reading IPAddressClaim %s: %w", claimName, getErr)
		}
		addressName, _, _ := unstructured.NestedString(claim.Object, "status", "addressRef", "name")
		if addressName == "" {
			waiting = append(waiting, claimName)
			continue
		}
		address := &unstructured.Unstructured{}
		address.SetGroupVersionKind(ipAddressGVK)
		getErr = r.reader().Get(ctx, types.NamespacedName{Namespace: vm.Namespace, Name: addressName}, address)
		switch {
		case getErr == nil:
		case kerrors.IsNotFound(getErr):
			waiting = append(waiting, claimName+" (IPAddress "+addressName+" not found yet)")
			continue
		default:
			return nil, nil, fmt.Errorf("reading IPAddress %s of claim %s: %w", addressName, claimName, getErr)
		}
		ip, perr := ipFromAddress(i, address)
		if perr != nil {
			return nil, nil, fmt.Errorf("IPAddress %s of claim %s: %w", addressName, claimName, perr)
		}
		ips = append(ips, ip)
	}
	return ips, waiting, nil
}

// ipFromAddress reads address, prefix and gateway off an IPAddress.
func ipFromAddress(index int, address *unstructured.Unstructured) (proxmox.NetworkIP, error) {
	addr, _, _ := unstructured.NestedString(address.Object, "spec", "address")
	if addr == "" {
		return proxmox.NetworkIP{}, fmt.Errorf("spec.address is empty")
	}
	prefix, found, _ := unstructured.NestedInt64(address.Object, "spec", "prefix")
	if !found || prefix <= 0 {
		return proxmox.NetworkIP{}, fmt.Errorf("spec.prefix is missing")
	}
	gateway, _, _ := unstructured.NestedString(address.Object, "spec", "gateway")
	return proxmox.NetworkIP{Index: index, Address: addr, Prefix: int(prefix), Gateway: gateway}, nil
}

// reconcileNetworkIPs writes the resolved addresses into ipconfigN, records
// the ports in status and says whether the machine may start: a claimed
// port without an address keeps it stopped, since the guest would come up
// without the address the spec promised it.
func (r *VirtualMachineReconciler) reconcileNetworkIPs(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine) (*ctrl.Result, error) {
	hasClaims := false
	for _, n := range networksOf(vm) {
		if n.IPAddressClaimRef != nil {
			hasClaims = true
			break
		}
	}
	if !hasClaims {
		return nil, nil
	}
	logger := log.FromContext(ctx)
	ips, waiting, err := r.resolveNetworkIPs(ctx, vm)
	if err != nil {
		reason := "Error"
		if strings.HasPrefix(err.Error(), reasonIPAMUnavailable) {
			reason = reasonIPAMUnavailable
		}
		r.networkEvent(vm, "Warning", reason, "%v", err)
		return nil, r.setNetworkCondition(ctx, vm, metav1.ConditionFalse, reason, err.Error())
	}

	changed, err := pc.ConfigureIPConfigs(vm, ips)
	if err != nil {
		r.networkEvent(vm, "Warning", "Error", "writing ipconfig: %v", err)
		return nil, err
	}
	if changed {
		logger.Info("Wrote ipconfig for claimed ports", "count", len(ips))
	}
	if err = r.recordNetworkStatus(ctx, pc, vm, ips); err != nil {
		return nil, err
	}
	if len(waiting) > 0 {
		msg := fmt.Sprintf("waiting for an IPAddress on claim %s", strings.Join(waiting, ", "))
		r.networkEvent(vm, "Normal", reasonWaitingForIPAddress, "%s", msg)
		if err = r.setNetworkCondition(ctx, vm, metav1.ConditionFalse, reasonWaitingForIPAddress, msg); err != nil {
			return nil, err
		}
		return &ctrl.Result{RequeueAfter: ipAddressRetry}, nil
	}
	return nil, r.setNetworkCondition(ctx, vm, metav1.ConditionTrue, reasonIPAddressesReady,
		fmt.Sprintf("%d claimed port(s) have their address", len(ips)))
}

// recordNetworkStatus writes status.networks: one entry per port with its
// MAC, and the address of the claimed ones.
func (r *VirtualMachineReconciler) recordNetworkStatus(ctx context.Context, pc *proxmox.ProxmoxClient,
	vm *proxmoxv1alpha1.VirtualMachine, ips []proxmox.NetworkIP) error {
	macs, err := pc.PortMACs(vm)
	if err != nil {
		return err
	}
	byIndex := map[int]proxmox.NetworkIP{}
	for _, ip := range ips {
		byIndex[ip.Index] = ip
	}
	networks := networksOf(vm)
	status := make([]proxmoxv1alpha1.VirtualMachineNetworkStatus, 0, len(networks))
	for i, n := range networks {
		entry := proxmoxv1alpha1.VirtualMachineNetworkStatus{Index: i, MACAddress: macs[i]}
		if n.IPAddressClaimRef != nil {
			entry.IPAddressClaim = n.IPAddressClaimRef.Name
		}
		if ip, ok := byIndex[i]; ok {
			entry.Address = fmt.Sprintf("%s/%d", ip.Address, ip.Prefix)
			entry.Gateway = ip.Gateway
		}
		status = append(status, entry)
	}
	sort.Slice(status, func(a, b int) bool { return status[a].Index < status[b].Index })
	if equalNetworkStatus(vm.Status.Networks, status) {
		return nil
	}
	patch := client.MergeFrom(vm.DeepCopy())
	vm.Status.Networks = status
	if err := r.Status().Patch(ctx, vm, patch); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func equalNetworkStatus(a, b []proxmoxv1alpha1.VirtualMachineNetworkStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *VirtualMachineReconciler) setNetworkCondition(ctx context.Context, vm *proxmoxv1alpha1.VirtualMachine,
	status metav1.ConditionStatus, reason, message string) error {
	existing := meta.FindStatusCondition(vm.Status.Conditions, typeNetworkReadyVirtualMachine)
	if existing != nil && existing.Status == status && existing.Reason == reason && existing.Message == message {
		return nil
	}
	patch := client.MergeFrom(vm.DeepCopy())
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type: typeNetworkReadyVirtualMachine, Status: status, Reason: reason, Message: message,
	})
	if err := r.Status().Patch(ctx, vm, patch); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func (r *VirtualMachineReconciler) networkEvent(vm *proxmoxv1alpha1.VirtualMachine, kind, reason, format string, args ...any) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(vm, nil, kind, reason, reason, format, args...)
}

// networksOf returns the networks of the spec, from the template or the
// scratch spec.
func networksOf(vm *proxmoxv1alpha1.VirtualMachine) []proxmoxv1alpha1.VirtualMachineNetwork {
	var networks *[]proxmoxv1alpha1.VirtualMachineNetwork
	switch {
	case vm.Spec.Template != nil:
		networks = vm.Spec.Template.Network
	case vm.Spec.VMSpec != nil:
		networks = vm.Spec.VMSpec.Network
	}
	if networks == nil {
		return nil
	}
	return *networks
}
