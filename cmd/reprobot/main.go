// Command reprobot checks security reports by running them.
package main

import (
	"fmt"
	"os"
)

const usage = `usage:
  reprobot run --project project.yaml [--cache DIR] [--json] repro.yaml
  reprobot serve [flags]
  reprobot worker [flags]
  reprobot token NAME >> tokens-file`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "run":
		os.Exit(runCmd(args))
	case "serve":
		os.Exit(serveCmd(args))
	case "worker":
		os.Exit(workerCmd(args))
	case "token":
		os.Exit(tokenCmd(args, os.Stdout, os.Stderr))
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
