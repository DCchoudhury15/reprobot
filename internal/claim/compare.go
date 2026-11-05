// Package claim compares what a reporter claimed with what really happened.
package claim

import (
	"fmt"
	"strings"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sanitizer"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// TopFrames is how many project frames a claimed frame is checked against.
const TopFrames = 5

// Compare checks a crash against the claim. match is true when the
// sanitizer and kind agree and, if the claim names a frame, that frame is
// among the top project frames. findings explains each mismatch in plain words.
func Compare(c spec.Claim, crash *result.Crash) (match bool, findings []string) {
	if crash == nil {
		return false, []string{"no crash was observed"}
	}
	if c.Sanitizer != crash.Sanitizer {
		findings = append(findings, fmt.Sprintf("claimed sanitizer %q but saw %q", c.Sanitizer, crash.Sanitizer))
	}
	if NormalizeKind(c.Kind) != NormalizeKind(crash.Kind) {
		findings = append(findings, fmt.Sprintf("claimed kind %q but saw %q", c.Kind, crash.Kind))
	}
	if c.Frame != "" {
		want := sanitizer.TrimArgs(c.Frame)
		var top []string
		for _, f := range sanitizer.ProjectFrames(crash) {
			if len(top) == TopFrames {
				break
			}
			top = append(top, sanitizer.TrimArgs(f.Function))
		}
		found := false
		for _, name := range top {
			if name == want {
				found = true
				break
			}
		}
		switch {
		case len(top) == 0:
			findings = append(findings, fmt.Sprintf("claimed frame %q but the crash has no project frames", c.Frame))
		case !found:
			findings = append(findings, fmt.Sprintf("claimed frame %q is not in the top %d project frames: %s",
				c.Frame, TopFrames, strings.Join(top, ", ")))
		}
	}
	return len(findings) == 0, findings
}

// NormalizeKind makes kind names comparable: lower case, with spaces and
// underscores turned into "-". "Heap Buffer Overflow" and
// "heap_buffer_overflow" both become "heap-buffer-overflow".
func NormalizeKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	return strings.NewReplacer(" ", "-", "_", "-").Replace(k)
}
