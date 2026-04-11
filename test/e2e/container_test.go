package e2e_test

// import (
// . "github.com/onsi/ginkgo/v2"
// . "github.com/onsi/gomega"

// proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
// "github.com/alperencelik/kubemox/test/e2e/framework"
// corev1 "k8s.io/api/core/v1"
// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
// )

// var _ = Describe("Container", func() {
// var container *proxmoxv1alpha1.Container

// AfterEach(func() {
// if container != nil {
// By("Deleting Container")
// err := fw.DeleteAndWaitGone(ctx, container, fw.Config.Timeouts.Cleanup)
// Expect(err).NotTo(HaveOccurred())
// }
// })

// It("should create a Container and populate status", func() {
// container = &proxmoxv1alpha1.Container{
// ObjectMeta: metav1.ObjectMeta{
// Name: "e2e-test-container",
// },
// Spec: proxmoxv1alpha1.ContainerSpec{
// Name:     "e2e-test-container",
// NodeName: "proxmox-dev",
// Template: proxmoxv1alpha1.ContainerTemplate{
// Cores:  1,
// Memory: 256,
// Disk: []proxmoxv1alpha1.ContainerTemplateDisk{
// {
// Storage: "local-lvm",
// Size:    4,
// Type:    "rootfs",
// },
// },
// Network: []proxmoxv1alpha1.ContainerTemplateNetwork{
// {
// Model:  "veth",
// Bridge: "vmbr0",
// },
// },
// },
// ConnectionRef: &corev1.LocalObjectReference{
// Name: framework.E2EProxmoxConnectionName,
// },
// },
// }

// By("Creating Container resource")
// Expect(fw.KubeClient.Create(ctx, container)).To(Succeed())

// By("Waiting for reconciliation to populate status")
// Eventually(func(g Gomega) {
// err := fw.KubeClient.Get(ctx, keyFromObject(container), container)
// g.Expect(err).NotTo(HaveOccurred())
// g.Expect(container.Status.Conditions).NotTo(BeEmpty(),
// "expected conditions to be populated after reconciliation")
// }).WithTimeout(fw.Config.Timeouts.ResourceReady).WithPolling(pollInterval).Should(Succeed())
// })
// })
