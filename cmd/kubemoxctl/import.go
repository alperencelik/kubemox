package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	kctl "github.com/alperencelik/kubemox/internal/kubemoxctl"
)

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	var (
		common commonFlags
		input  string
		paused bool
	)
	registerCommonFlags(fs, &common)
	fs.StringVar(&input, "i", "", "Input file (default: stdin).")
	fs.BoolVar(&paused, "paused", true, "Create CRs with Disable reconcile-mode annotation set (safe default).")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dyn, _, err := kctl.NewClient(common.kubeconfig, common.context)
	if err != nil {
		return err
	}
	ctx := context.Background()

	var in io.Reader = os.Stdin
	if input != "" {
		f, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("open %s: %w", input, err)
		}
		defer f.Close()
		in = f
	}

	objs, err := kctl.ReadArchive(in)
	if err != nil {
		return err
	}
	if err := kctl.ImportAll(ctx, dyn, objs, paused); err != nil {
		return err
	}
	note := ""
	if paused {
		note = " (paused — run 'kubemoxctl resume' when ready)"
	}
	fmt.Fprintf(os.Stderr, "created %d objects%s\n", len(objs), note)
	return nil
}
