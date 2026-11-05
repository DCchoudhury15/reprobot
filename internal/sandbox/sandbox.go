// Package sandbox runs one command in a fresh, locked-down container.
package sandbox

import (
	"context"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Mount is a bind mount into the container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// Spec describes one sandboxed command.
type Spec struct {
	Image        string
	Cmd          []string
	User         string // "uid:gid"; empty means the image default
	Env          map[string]string
	WorkDir      string
	Mounts       []Mount
	Network      bool // false means no network at all
	CPUs         float64
	MemoryMB     int
	PidsLimit    int
	Timeout      time.Duration
	ReadOnlyRoot bool
	TmpfsMB      int // size of the /tmp tmpfs
}

// Outcome is what happened.
type Outcome struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	TimedOut  bool
	OOMKilled bool
	Duration  time.Duration
}

// Runtime returns "runsc" when the gVisor runtime is registered with
// Docker, otherwise "runc".
func Runtime(ctx context.Context) (string, error) { return "", spec.ErrNotImplemented }

// Run starts one fresh container, waits for it, and always removes it.
func Run(ctx context.Context, s Spec) (Outcome, error) {
	return Outcome{}, spec.ErrNotImplemented
}

// ImageDigest returns the content digest of an image, pulling it if needed.
func ImageDigest(ctx context.Context, image string) (string, error) {
	return "", spec.ErrNotImplemented
}
