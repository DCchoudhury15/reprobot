// Package source fetches project git repos and exports exact commits.
package source

import (
	"context"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Resolve fetches repo (URL or local path) into a mirror under cacheDir and
// returns the mirror path and the full commit sha for ref (sha or tag).
func Resolve(ctx context.Context, cacheDir, repo, ref string) (mirrorDir, sha string, err error) {
	return "", "", spec.ErrNotImplemented
}

// Export writes the tree at sha into dest, without the .git directory.
func Export(ctx context.Context, mirrorDir, sha, dest string) error {
	return spec.ErrNotImplemented
}
