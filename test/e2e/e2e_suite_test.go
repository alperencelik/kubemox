package e2e_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/alperencelik/kubemox/test/e2e/framework"
)

var (
	fw  *framework.Framework
	ctx context.Context
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "kubemox E2E Suite")
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	var err error
	fw, err = framework.NewFramework("config/e2e-config.yaml")
	Expect(err).NotTo(HaveOccurred(), "failed to create e2e framework")

	By("Creating ProxmoxConnection CR")
	Expect(fw.EnsureProxmoxConnection(ctx)).To(Succeed())

	By("Waiting for operator deployment to be available")
	Expect(fw.WaitForOperatorReady(ctx, 3*time.Minute)).To(Succeed())
})

var _ = AfterSuite(func() {
	if fw != nil {
		By("Cleaning up e2e resources")
		fw.Cleanup(ctx)
	}
})
