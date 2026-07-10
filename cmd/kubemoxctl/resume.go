package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	kctl "github.com/alperencelik/kubemox/internal/kubemoxctl"
)

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	var common commonFlags
	registerCommonFlags(fs, &common)
	if err := fs.Parse(args); err != nil {
		return err
	}

	dyn, _, err := kctl.NewClient(common.kubeconfig, common.context)
	if err != nil {
		return err
	}
	n, err := kctl.ResumeAll(context.Background(), dyn)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "resumed %d objects\n", n)
	return nil
}
