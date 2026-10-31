package proxmox

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

var _ = Describe("enableAutoStart", func() {
	ctx := context.Background()

	create := func(name string, autoStart *bool) *proxmoxv1alpha1.VirtualMachine {
		vm := &proxmoxv1alpha1.VirtualMachine{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: metav1.NamespaceDefault},
			Spec: proxmoxv1alpha1.VirtualMachineSpec{
				Name:            name,
				NodeName:        "pve1",
				EnableAutoStart: autoStart,
				VMSpec:          &proxmoxv1alpha1.NewVMSpec{Cores: 1, Socket: 1, Memory: 512},
				ConnectionRef:   &corev1.LocalObjectReference{Name: "conn"},
			},
		}
		Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, vm))).To(Succeed()) })
		return vm
	}

	It("keeps false through the Update that adds the finalizer", func() {
		vm := create("autostart-false", ptr.To(false))
		vm.Finalizers = []string{virtualMachineFinalizerName}
		Expect(k8sClient.Update(ctx, vm)).To(Succeed())

		got := &proxmoxv1alpha1.VirtualMachine{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(vm), got)).To(Succeed())
		Expect(got.Spec.EnableAutoStart).To(HaveValue(BeFalse()))

		got.Finalizers = nil
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
	})

	It("defaults to true when unset", func() {
		vm := create("autostart-unset", nil)
		Expect(vm.Spec.EnableAutoStart).To(HaveValue(BeTrue()))
	})

	// The nil Proxmox client panics on any attempt to start the machine.
	It("does not start a machine when false", func() {
		r := &VirtualMachineReconciler{}
		vm := &proxmoxv1alpha1.VirtualMachine{}
		vm.Spec.EnableAutoStart = ptr.To(false)

		res, err := r.startCreatedVM(ctx, nil, vm, "pve1")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.IsZero()).To(BeTrue())

		res, err = r.handleAutoStart(ctx, nil, vm)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.IsZero()).To(BeTrue())
	})
})
