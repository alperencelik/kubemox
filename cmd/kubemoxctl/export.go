package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	kctl "github.com/alperencelik/kubemox/internal/kubemoxctl"
)

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var (
		common commonFlags
		output string
		pause  bool
	)
	registerCommonFlags(fs, &common)
	fs.StringVar(&output, "o", "", "Output file (default: stdout).")
	fs.BoolVar(&pause, "pause-source", false, "Stamp Disable reconcile-mode annotation on source CRs before export.")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dyn, _, err := kctl.NewClient(common.kubeconfig, common.context)
	if err != nil {
		return err
	}
	ctx := context.Background()

	var out io.Writer = os.Stdout
	if output != "" {
		f, err := os.Create(output)
		if err != nil {
			return fmt.Errorf("create %s: %w", output, err)
		}
		defer f.Close()
		out = f
	}

	if pause {
		if err := kctl.PauseAll(ctx, dyn); err != nil {
			return err
		}
	}

	objs, err := kctl.ExportAll(ctx, dyn)
	if err != nil {
		return err
	}
	if err := kctl.WriteArchive(out, objs); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d objects\n", len(objs))
	return nil
}
