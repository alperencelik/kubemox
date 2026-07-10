package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"

	kctl "github.com/alperencelik/kubemox/internal/kubemoxctl"
)

func runMove(args []string) error {
	fs := flag.NewFlagSet("move", flag.ExitOnError)
	var (
		// Source: current kubeconfig/context unless overridden. Same shape as
		// every other kubectl-style tool, so no --from is needed.
		srcKubeconfig string
		srcContext    string
		// Target: a separate kubeconfig (clusterctl-style) or a context in the
		// same kubeconfig. At least one must be set.
		dstKubeconfig string
		dstContext    string
		archive       string
		pauseSrc      bool
		resumeDst     bool
	)
	fs.StringVar(&srcKubeconfig, "kubeconfig", "", "Source kubeconfig path (falls back to KUBECONFIG env / ~/.kube/config).")
	fs.StringVar(&srcContext, "context", "", "Source kubeconfig context (falls back to current-context).")
	fs.StringVar(&dstKubeconfig, "to-kubeconfig", "", "Target kubeconfig path.")
	fs.StringVar(&dstContext, "to-context", "", "Target context name. Used with --to-kubeconfig, or alone to pick a context from the source kubeconfig.")
	fs.StringVar(&archive, "archive", "", "Optional path to write a backup YAML archive alongside the move.")
	fs.BoolVar(&pauseSrc, "pause-source", true, "Stamp Disable on source CRs before copying (recommended).")
	fs.BoolVar(&resumeDst, "resume-target", true, "Clear Disable on target CRs once the copy succeeds.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if dstKubeconfig == "" && dstContext == "" {
		return fmt.Errorf("specify a target: --to-kubeconfig <path> and/or --to-context <name>")
	}

	ctx := context.Background()

	srcDyn, _, err := kctl.NewClient(srcKubeconfig, srcContext)
	if err != nil {
		return fmt.Errorf("source client: %w", err)
	}

	// If --to-kubeconfig is omitted, the target lives in the same kubeconfig
	// as the source — reuse srcKubeconfig so KUBECONFIG-less invocations keep
	// working.
	dstKubeconfigPath := dstKubeconfig
	if dstKubeconfigPath == "" {
		dstKubeconfigPath = srcKubeconfig
	}
	dstDyn, _, err := kctl.NewClient(dstKubeconfigPath, dstContext)
	if err != nil {
		return fmt.Errorf("target client: %w", err)
	}

	srcLabel := describeTarget(srcKubeconfig, srcContext)
	dstLabel := describeTarget(dstKubeconfig, dstContext)
	if srcLabel == dstLabel {
		return fmt.Errorf("source and target resolve to the same cluster (%s)", srcLabel)
	}

	if pauseSrc {
		fmt.Fprintf(os.Stderr, "pausing source (%s)...\n", srcLabel)
		if err := kctl.PauseAll(ctx, srcDyn); err != nil {
			return fmt.Errorf("pause source: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "exporting from %s...\n", srcLabel)
	objs, err := kctl.ExportAll(ctx, srcDyn)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	fmt.Fprintf(os.Stderr, "collected %d objects\n", len(objs))

	if archive != "" {
		var buf bytes.Buffer
		if err := kctl.WriteArchive(&buf, objs); err != nil {
			return fmt.Errorf("serialize archive: %w", err)
		}
		if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write archive %s: %w", archive, err)
		}
		fmt.Fprintf(os.Stderr, "wrote backup archive to %s\n", archive)
	}

	fmt.Fprintf(os.Stderr, "importing into %s (paused)...\n", dstLabel)
	if err := kctl.ImportAll(ctx, dstDyn, objs, true); err != nil {
		return fmt.Errorf("import: %w (source is still paused — fix the target and re-run, or run 'kubemoxctl resume' against the source to roll back)", err)
	}

	if resumeDst {
		fmt.Fprintf(os.Stderr, "resuming target (%s)...\n", dstLabel)
		n, err := kctl.ResumeAll(ctx, dstDyn)
		if err != nil {
			return fmt.Errorf("resume target: %w", err)
		}
		fmt.Fprintf(os.Stderr, "resumed %d objects on target\n", n)
	}

	fmt.Fprintf(os.Stderr, "done. Source (%s) is left paused; delete the source CRs manually once you've verified the target.\n", srcLabel)
	return nil
}

// describeTarget returns a short label ("context @ kubeconfig") used in log
// messages and duplicate-target detection.
func describeTarget(kubeconfig, context string) string {
	cfg := kubeconfig
	if cfg == "" {
		cfg = "default"
	}
	ctx := context
	if ctx == "" {
		ctx = "current-context"
	}
	return ctx + "@" + cfg
}
