// Package source fetches project git repos and exports exact commits.
package source

import (
	"context"
	"errors"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// ErrUnknownRef means the repo was fetched but the ref (sha or tag) does
// not exist in it. Callers treat it as bad input, not an infrastructure error.
var ErrUnknownRef = errors.New("unknown ref")

// Resolve fetches repo (URL or local path) into a mirror under cacheDir and
// returns the mirror path and the full commit sha for ref (sha or tag).
func Resolve(ctx context.Context, cacheDir, repo, ref string) (mirrorDir, sha string, err error) {
	return "", "", spec.ErrNotImplemented
}

// Export writes the tree at sha into dest, without the .git directory.
func Export(ctx context.Context, mirrorDir, sha, dest string) error {
	return spec.ErrNotImplemented
}
