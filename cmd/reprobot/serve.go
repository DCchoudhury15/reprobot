package main

import (
	"fmt"
	"os"
)

func serveCmd(args []string) int {
	fmt.Fprintln(os.Stderr, "reprobot serve: not implemented yet")
	return 2
}

func workerCmd(args []string) int {
	fmt.Fprintln(os.Stderr, "reprobot worker: not implemented yet")
	return 2
}
