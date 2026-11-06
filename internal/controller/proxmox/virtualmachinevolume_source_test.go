package proxmox

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

const (
	sourceTestNode   = "pve1"
	sourceTestVolid  = "local-zfs:vm-9998-disk-0"
	sourceTestImages = "cozy-public"
	sourceTestGolden = "golden"
	sourceTestTenant = "team-a"
	sourceTestBase   = "base"
)

func volumeFixture(namespace, name, node string, cloneable bool) *proxmoxv1alpha1.VirtualMachineVolume {
	v := &proxmoxv1alpha1.VirtualMachineVolume{}
	v.Namespace = namespace
	v.Name = name
	v.Spec.NodeName = node
	v.Spec.Storage = "local-zfs"
	v.Spec.Size = 10
	v.Spec.Cloneable = cloneable
	v.Status.Phase = proxmoxv1alpha1.VirtualMachineVolumePhaseReady
	v.Status.Volume = sourceTestVolid
	return v
}

func sourceTestReconciler(objs ...client.Object) *VirtualMachineVolumeReconciler {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(proxmoxv1alpha1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithStatusSubresource(&proxmoxv1alpha1.VirtualMachineVolume{}).Build()
	return &VirtualMachineVolumeReconciler{Client: cl, Scheme: scheme}
}

// TestResolveSourceAcrossNamespaces pins the rules for source.disk: a volume
// of the same namespace is copied as soon as it is Ready and free, a volume
// of another namespace only when its owner marked it cloneable, and a node
// mismatch is final.
func TestResolveSourceAcrossNamespaces(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		source     *proxmoxv1alpha1.VirtualMachineVolume
		ref        proxmoxv1alpha1.VirtualMachineVolumeDiskSource
		wantVolid  string
		wantReason string
		wantPhase  string
		wantRetry  bool
	}{
		{
			name:      "same namespace needs no flag",
			source:    volumeFixture(sourceTestTenant, sourceTestBase, sourceTestNode, false),
			ref:       proxmoxv1alpha1.VirtualMachineVolumeDiskSource{Name: sourceTestBase},
			wantVolid: sourceTestVolid,
		},
		{
			name:       "another namespace waits until the source is cloneable",
			source:     volumeFixture(sourceTestImages, sourceTestGolden, sourceTestNode, false),
			ref:        proxmoxv1alpha1.VirtualMachineVolumeDiskSource{Name: sourceTestGolden, Namespace: sourceTestImages},
			wantReason: reasonVolumeSourceKept,
			wantPhase:  proxmoxv1alpha1.VirtualMachineVolumePhasePending,
			wantRetry:  true,
		},
		{
			name:      "another namespace copies a cloneable source",
			source:    volumeFixture(sourceTestImages, sourceTestGolden, sourceTestNode, true),
			ref:       proxmoxv1alpha1.VirtualMachineVolumeDiskSource{Name: sourceTestGolden, Namespace: sourceTestImages},
			wantVolid: sourceTestVolid,
		},
		{
			name:       "a missing source is waited for",
			source:     nil,
			ref:        proxmoxv1alpha1.VirtualMachineVolumeDiskSource{Name: "nowhere", Namespace: sourceTestImages},
			wantReason: reasonVolumeSourceWaiting,
			wantPhase:  proxmoxv1alpha1.VirtualMachineVolumePhasePending,
			wantRetry:  true,
		},
		{
			name:       "a source on another node is final",
			source:     volumeFixture(sourceTestImages, sourceTestGolden, "pve2", true),
			ref:        proxmoxv1alpha1.VirtualMachineVolumeDiskSource{Name: sourceTestGolden, Namespace: sourceTestImages},
			wantReason: reasonVolumeFailed,
			wantPhase:  proxmoxv1alpha1.VirtualMachineVolumePhaseFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vol := volumeFixture(sourceTestTenant, "copy", sourceTestNode, false)
			vol.Status = proxmoxv1alpha1.VirtualMachineVolumeStatus{}
			ref := tc.ref
			vol.Spec.Source = &proxmoxv1alpha1.VirtualMachineVolumeSource{Disk: &ref}
			objs := []client.Object{vol}
			if tc.source != nil {
				objs = append(objs, tc.source)
			}
			r := sourceTestReconciler(objs...)

			volid, res, err := r.resolveSource(ctx, vol)
			if err != nil {
				t.Fatalf("resolveSource: %v", err)
			}
			if volid != tc.wantVolid {
				t.Fatalf("volid = %q, want %q", volid, tc.wantVolid)
			}
			if tc.wantRetry != (res != nil && res.RequeueAfter > 0) {
				t.Fatalf("retry = %v, want %v", res, tc.wantRetry)
			}
			if tc.wantReason == "" {
				return
			}
			got := &proxmoxv1alpha1.VirtualMachineVolume{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(vol), got); err != nil {
				t.Fatal(err)
			}
			cond := meta.FindStatusCondition(got.Status.Conditions, typeReadyVirtualMachineVolume)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != tc.wantReason {
				t.Fatalf("condition = %+v, want reason %s", cond, tc.wantReason)
			}
			if got.Status.Phase != tc.wantPhase {
				t.Fatalf("phase = %q, want %q", got.Status.Phase, tc.wantPhase)
			}
		})
	}
}
