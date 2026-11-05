// Package claim compares what a reporter claimed with what really happened.
package claim

import (
	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Compare checks a crash against the claim. match is true when the
// sanitizer and kind agree and, if the claim names a frame, that frame is
// among the top project frames. findings explains each mismatch in plain words.
func Compare(c spec.Claim, crash *result.Crash) (match bool, findings []string) {
	return false, []string{"not implemented"}
}
