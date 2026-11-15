// Command reprobot-replay runs scripted TCP replay servers inside a helper
// container. The runner starts this binary in a network-less container, then
// gives the program container --network=container:<helper> so it can reach
// the servers on loopback while external access stays blocked.
//
// Usage: reprobot-replay -spec spec.json -files /dir
//
// It prints "READY\n" to stdout once all listeners are up, then blocks until
// SIGTERM or SIGINT.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/DCchoudhury15/reprobot/internal/replay"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

func main() {
	specFile := flag.String("spec", "", "path to JSON file with []spec.Server")
	filesDir := flag.String("files", ".", "directory containing the send-step files")
	flag.Parse()

	if *specFile == "" {
		fmt.Fprintln(os.Stderr, "reprobot-replay: -spec is required")
		os.Exit(1)
	}

	b, err := os.ReadFile(*specFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reprobot-replay: %v\n", err)
		os.Exit(1)
	}
	var servers []spec.Server
	if err := json.Unmarshal(b, &servers); err != nil {
		fmt.Fprintf(os.Stderr, "reprobot-replay: parse spec: %v\n", err)
		os.Exit(1)
	}

	g, err := replay.New(servers, *filesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reprobot-replay: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := g.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "reprobot-replay: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("READY")

	<-ctx.Done()
	g.Stop()
}
