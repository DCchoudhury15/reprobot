// Package sanitizer parses AddressSanitizer and UndefinedBehaviorSanitizer
// output into a result.Crash with a stable signature.
package sanitizer

import "github.com/DCchoudhury15/reprobot/internal/result"

// Parse finds a sanitizer report in stderr. It returns nil when there is none.
func Parse(stderr string) *result.Crash { return nil }
