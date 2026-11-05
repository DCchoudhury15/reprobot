// Package claimcheck does the cheap static check: do the functions, files
// and file:line references in the claim exist at the reported commit?
package claimcheck

import (
	"context"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Check looks for code references in the claim and returns one finding for
// each reference that does not exist at sha. mirrorDir is a git repo
// (bare is fine) that contains sha.
func Check(ctx context.Context, mirrorDir, sha string, c spec.Claim) ([]string, error) {
	return nil, spec.ErrNotImplemented
}
