package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `kubemoxctl — move kubemox custom resources between clusters.

Usage:
  kubemoxctl move   [flags]   Pause source, copy all kubemox CRs to target, resume target.
  kubemoxctl export [flags]   Export kubemox CRs from the current cluster to a YAML archive.
  kubemoxctl import [flags]   Create kubemox CRs from a YAML archive on the current cluster.
  kubemoxctl resume [flags]   Clear the Disable reconcile-mode annotation from kubemox CRs.

Run 'kubemoxctl <command> -h' for command-specific flags.
`

type commonFlags struct {
	kubeconfig string
	context    string
}

func registerCommonFlags(fs *flag.FlagSet, c *commonFlags) {
	fs.StringVar(&c.kubeconfig, "kubeconfig", "", "Path to kubeconfig (falls back to KUBECONFIG env / ~/.kube/config).")
	fs.StringVar(&c.context, "context", "", "Kubeconfig context name to use.")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "move":
		err = runMove(args)
	case "export":
		err = runExport(args)
	case "import":
		err = runImport(args)
	case "resume":
		err = runResume(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
