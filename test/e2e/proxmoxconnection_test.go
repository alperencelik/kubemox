package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/alperencelik/kubemox/test/e2e/framework"
)

var _ = Describe("ProxmoxConnection", func() {
	It("should connect to Proxmox and report version in status", func() {
		By("Waiting for ProxmoxConnection to become Ready")
		conn, err := fw.GetProxmoxConnection(ctx, framework.E2EProxmoxConnectionName)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			conn, err = fw.GetProxmoxConnection(ctx, framework.E2EProxmoxConnectionName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(framework.IsProxmoxConnectionReady(conn)).To(BeTrue(),
				"expected ProxmoxConnection to be Ready")
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("Verifying status fields are populated")
		Expect(conn.Status.Version).NotTo(BeEmpty(), "expected Proxmox version in status")
		Expect(conn.Status.ConnectionStatus).NotTo(BeEmpty(), "expected connection status")
	})
})
