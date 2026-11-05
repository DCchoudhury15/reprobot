// Package runner checks one reproducer end to end.
package runner

import (
	"context"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Options for one check.
type Options struct {
	ProjectPath string // maintainer project config
	ReproPath   string // reporter repro.yaml; its files sit next to it
	CacheDir    string // git mirrors and build cache
}

// Run checks one reproducer and returns the verdict. Bad input gives a
// Result with State INVALID_INPUT, not an error. Errors are only for
// infrastructure problems (Docker down, disk full).
func Run(ctx context.Context, o Options) (*result.Result, error) {
	return nil, spec.ErrNotImplemented
}
