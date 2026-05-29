// Command apiserver runs the kubemox aggregated apiserver. It serves the
// ops.proxmox.alperen.cloud API group, projecting live Proxmox state and
// imperative operations onto the Kubernetes API surface.
//
// In-cluster, this binary is fronted by a Service and registered with the
// parent kube-apiserver via an APIService resource. See config/apiserver/
// for the deployment manifests.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alperencelik/kubemox/pkg/apiserver/server"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	cmd := server.NewCommand(ctx)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "apiserver: %v\n", err)
		os.Exit(1)
	}
}
