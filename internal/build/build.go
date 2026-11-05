// Package build builds a project commit with a maintainer profile, with a
// content-addressed cache.
package build

import (
	"context"
	"errors"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// ErrBuildFailed means the profile did not build at that commit. Output
// still carries the log tail.
var ErrBuildFailed = errors.New("build failed")

// Builder builds in a sandbox and caches outputs under CacheDir.
type Builder struct {
	CacheDir string
}

// Output describes a finished (or cached) build.
type Output struct {
	BinDir      string // directory holding the profile outputs
	Key         string
	CacheHit    bool
	LogTail     string
	ImageDigest string
	Runtime     string // runsc | runc
}

// Key returns the build cache key: sha256 hex of project id, commit,
// profile name, profile content and image digest.
func Key(projectID, sha, profileName string, profile spec.Profile, imageDigest string) string {
	return ""
}

// Build builds sha with the named profile, or returns cached outputs.
func (b *Builder) Build(ctx context.Context, p *spec.Project, profileName, sha, mirrorDir string) (Output, error) {
	return Output{}, spec.ErrNotImplemented
}
