package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/test/e2e/framework"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("VirtualMachine", func() {
	Context("from VirtualMachineTemplate", Ordered, func() {
		var (
			vmTemplate *proxmoxv1alpha1.VirtualMachineTemplate
			vm         *proxmoxv1alpha1.VirtualMachine
		)

		const templateName = "e2e-test-template"

		AfterAll(func() {
			if vm != nil {
				By("Deleting VirtualMachine")
				err := fw.DeleteAndWaitGone(ctx, vm, fw.Config.Timeouts.Cleanup)
				Expect(err).NotTo(HaveOccurred())
			}
			if vmTemplate != nil {
				By("Deleting VirtualMachineTemplate")
				err := fw.DeleteAndWaitGone(ctx, vmTemplate, fw.Config.Timeouts.ProxmoxOperation)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should create a VirtualMachineTemplate and auto-create StorageDownloadURL", func() {
			vmTemplate = &proxmoxv1alpha1.VirtualMachineTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: templateName,
				},
				Spec: proxmoxv1alpha1.VirtualMachineTemplateSpec{
					Name:     templateName,
					NodeName: fw.Config.Proxmox.NodeName,
					VirtualMachineConfig: proxmoxv1alpha1.VirtualMachineConfig{
						Cores:   1,
						Sockets: 1,
						Memory:  512,
					},
					ImageConfig: proxmoxv1alpha1.StorageDownloadURLContent{
						Content:  "iso",
						Filename: "e2e-test.iso",
						Node:     fw.Config.Proxmox.NodeName,
						Storage:  "local",
						URL:      "https://download.cirros-cloud.net/0.6.2/cirros-0.6.2-x86_64-disk.img",
					},
					ConnectionRef: &corev1.LocalObjectReference{
						Name: framework.E2EProxmoxConnectionName,
					},
				},
			}

			By("Creating VirtualMachineTemplate resource")
			Expect(fw.KubeClient.Create(ctx, vmTemplate)).To(Succeed())

			By("Verifying StorageDownloadURL child resource is created")
			sdu := &proxmoxv1alpha1.StorageDownloadURL{}
			Eventually(func(g Gomega) {
				err := fw.KubeClient.Get(ctx, client.ObjectKey{Name: templateName + "-image"}, sdu)
				g.Expect(err).NotTo(HaveOccurred())
			}).WithTimeout(fw.Config.Timeouts.ResourceReady).WithPolling(pollInterval).Should(Succeed())
		})

		It("should populate VirtualMachineTemplate status conditions", func() {
			Eventually(func(g Gomega) {
				err := fw.KubeClient.Get(ctx, keyFromObject(vmTemplate), vmTemplate)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(vmTemplate.Status.Conditions).NotTo(BeEmpty(),
					"expected conditions to be populated after reconciliation")
			}).WithTimeout(fw.Config.Timeouts.ProxmoxOperation).WithPolling(pollInterval).Should(Succeed())
		})

		It("should create a VirtualMachine from the template and populate status", func() {
			vm = &proxmoxv1alpha1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name: "e2e-test-vm-from-template",
				},
				Spec: proxmoxv1alpha1.VirtualMachineSpec{
					Name:     "e2e-test-vm-from-template",
					NodeName: fw.Config.Proxmox.NodeName,
					Template: &proxmoxv1alpha1.VirtualMachineSpecTemplate{
						Name:   templateName,
						Cores:  1,
						Socket: 1,
						Memory: 512,
					},
					ConnectionRef: &corev1.LocalObjectReference{
						Name: framework.E2EProxmoxConnectionName,
					},
				},
			}

			By("Creating VirtualMachine from template")
			Expect(fw.KubeClient.Create(ctx, vm)).To(Succeed())

			By("Waiting for reconciliation to populate status")
			Eventually(func(g Gomega) {
				err := fw.KubeClient.Get(ctx, keyFromObject(vm), vm)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(vm.Status.Conditions).NotTo(BeEmpty(),
					"expected conditions to be populated after reconciliation")
			}).WithTimeout(fw.Config.Timeouts.ProxmoxOperation).WithPolling(pollInterval).Should(Succeed())
		})
	})
})
