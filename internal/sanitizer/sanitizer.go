// Package sanitizer parses AddressSanitizer and UndefinedBehaviorSanitizer
// output into a result.Crash with a stable signature.
//
// The parser reads the first report in stderr. For ASan that is the block
// that starts with "==PID==ERROR: AddressSanitizer: <kind> ...", for UBSan
// the first "file:line:col: runtime error: <message>" line. Both gcc and
// clang print these formats.
//
// UBSan prints a stack trace only with UBSAN_OPTIONS=print_stacktrace=1.
// Without it the crash has a single frame (the error location, no function)
// and the signature falls back to kind plus file base name, which is much
// weaker. Build profiles that use UBSan should set print_stacktrace=1.
//
// Profiles should link the sanitizer runtime as a shared library (the gcc
// default). With gcc -static-libasan, interceptor frames such as memcpy sit
// inside the program binary and look like project frames.
package sanitizer

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

var (
	asanErrorRe   = regexp.MustCompile(`==\d+==ERROR: AddressSanitizer: (\S+)(?:\s+(\S+))?`)
	asanSummaryRe = regexp.MustCompile(`^SUMMARY: AddressSanitizer: ([A-Za-z][A-Za-z0-9_-]*)`)
	accessRe      = regexp.MustCompile(`^(READ|WRITE) of size (\d+) at `)
	signalRe      = regexp.MustCompile(`The signal is caused by a (READ|WRITE) memory access`)
	ubsanRe       = regexp.MustCompile(`^(?:(.+?):(\d+):(\d+)|<unknown>): runtime error: (.+)$`)
	frameRe       = regexp.MustCompile(`^\s*#(\d+)\s+0x[0-9a-fA-F]+\s*(.*)$`)
	buildIDRe     = regexp.MustCompile(`\s*\(BuildId: [0-9a-fA-F]+\)$`)
	moduleRe      = regexp.MustCompile(`\s*\(([^()\s]+)\+0x[0-9a-fA-F]+\)$`)
	locationRe    = regexp.MustCompile(`(?:^|\s)(\S+?):(\d+)(?::(\d+))?$`)
)

// maxGap is how many non-frame lines may sit between a report header and
// its first stack frame (UBSan notes, ASan SEGV hints).
const maxGap = 8

// Parse finds a sanitizer report in stderr. It returns nil when there is none.
// When stderr has several reports, the first one wins.
func Parse(stderr string) *result.Crash {
	lines := strings.Split(stderr, "\n")
	for i, l := range lines {
		if m := asanErrorRe.FindStringSubmatch(l); m != nil {
			return finish(parseASan(lines, i, m))
		}
		if m := ubsanRe.FindStringSubmatch(l); m != nil {
			return finish(parseUBSan(lines, i, m))
		}
	}
	return nil
}

func parseASan(lines []string, at int, m []string) *result.Crash {
	c := &result.Crash{Sanitizer: "address", Kind: asanKind(m[1], m[2])}
	for _, l := range lines[at+1:] {
		if isReportStart(l) {
			break
		}
		if s := asanSummaryRe.FindStringSubmatch(l); s != nil {
			// The summary names the bug type even when the header does not,
			// for example "requested allocation size ... exceeds maximum".
			c.Kind = s[1]
			break
		}
	}
	for _, l := range lines[at+1:] {
		if frameRe.MatchString(l) {
			break
		}
		if a := accessRe.FindStringSubmatch(l); a != nil {
			c.Access = a[1]
			c.Size, _ = strconv.Atoi(a[2])
			break
		}
		if a := signalRe.FindStringSubmatch(l); a != nil {
			c.Access = a[1]
			break
		}
	}
	c.Frames = firstStack(lines, at+1)
	return c
}

// asanKind reads the bug type from the ERROR line.
func asanKind(first, second string) string {
	if first == "attempting" {
		switch second {
		case "double-free":
			return "double-free"
		case "free":
			return "bad-free"
		}
	}
	return first
}

func parseUBSan(lines []string, at int, m []string) *result.Crash {
	c := &result.Crash{Sanitizer: "undefined", Kind: ubsanKind(m[4])}
	c.Frames = firstStack(lines, at+1)
	if len(c.Frames) == 0 && m[1] != "" {
		line, _ := strconv.Atoi(m[2])
		c.Frames = []result.Frame{{Index: 0, File: m[1], Line: line}}
	}
	return c
}

// ubsanKind maps a UBSan message to a short kind name.
func ubsanKind(msg string) string {
	has := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(msg, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("signed integer overflow"):
		return "signed-integer-overflow"
	case has("shift exponent", "left shift of"):
		return "shift-out-of-bounds"
	case has("load of null pointer", "store to null pointer",
		"member access within null pointer", "member call on null pointer"):
		return "null-pointer-dereference"
	case has("index ") && strings.Contains(msg, " out of bounds"):
		return "index-out-of-bounds"
	case strings.Contains(msg, "misaligned address"):
		return "misaligned-address"
	case has("division by zero"):
		return "division-by-zero"
	}
	return "undefined-behavior"
}

func isReportStart(l string) bool {
	return asanErrorRe.MatchString(l) || ubsanRe.MatchString(l)
}

// firstStack returns the first run of frame lines that starts within
// maxGap lines of from. It stops at the first line that is not a frame, so
// later stacks ("allocated by", "freed by") are not included.
func firstStack(lines []string, from int) []result.Frame {
	var frames []result.Frame
	gap := 0
	for _, l := range lines[from:] {
		m := frameRe.FindStringSubmatch(l)
		if m == nil {
			if len(frames) > 0 {
				break
			}
			if gap++; gap > maxGap || isReportStart(l) {
				break
			}
			continue
		}
		f := parseFrame(m[2])
		f.Index, _ = strconv.Atoi(m[1])
		frames = append(frames, f)
	}
	return frames
}

// parseFrame reads the part of a frame line after the address, for example
// "in parse_header /src/tinyparse.c:21" or
// "in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)" or
// " (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b...)".
func parseFrame(rest string) result.Frame {
	var f result.Frame
	rest = strings.TrimSpace(buildIDRe.ReplaceAllString(rest, ""))
	if loc := moduleRe.FindStringSubmatchIndex(rest); loc != nil {
		f.Module = rest[loc[2]:loc[3]]
		rest = rest[:loc[0]]
	} else if loc := locationRe.FindStringSubmatchIndex(rest); loc != nil {
		f.File = rest[loc[2]:loc[3]]
		f.Line, _ = strconv.Atoi(rest[loc[4]:loc[5]])
		rest = rest[:loc[0]]
	}
	if fn, ok := strings.CutPrefix(strings.TrimSpace(rest), "in "); ok {
		f.Function = strings.TrimSpace(fn)
	}
	return f
}

// skipFunctions are runtime function name prefixes that never belong to
// the project. "_start" is matched exactly.
var skipFunctions = []string{"__asan", "__interceptor_", "___interceptor_",
	"__sanitizer", "__ubsan", "__libc_"}

// systemLibs are shared libraries whose frames never belong to the project.
var systemLibs = []string{"libc", "libasan", "libubsan", "libstdc++", "libgcc_s", "libgcc"}

// ProjectFrames returns the frames that belong to the project: it drops
// frames without a function name, sanitizer runtime and libc start-up
// functions, and frames inside libc, libasan, libubsan, libstdc++, libgcc
// or the dynamic loader.
func ProjectFrames(c *result.Crash) []result.Frame {
	if c == nil {
		return nil
	}
	var out []result.Frame
	for _, f := range c.Frames {
		if f.Function == "" || f.Function == "_start" || systemModule(f.Module) {
			continue
		}
		skip := false
		for _, p := range skipFunctions {
			if strings.HasPrefix(f.Function, p) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}

func systemModule(module string) bool {
	if module == "" {
		return false
	}
	base := path.Base(module)
	if strings.HasPrefix(base, "ld-linux") {
		return true
	}
	for _, lib := range systemLibs {
		if strings.HasPrefix(base, lib+".so") ||
			(strings.HasPrefix(base, lib+"-") && strings.Contains(base, ".so")) {
			return true
		}
	}
	return false
}

// TrimArgs drops a trailing C++ argument list and cv or ref qualifiers
// from a function name: "ns::Buf::at(unsigned long) const" becomes
// "ns::Buf::at". Plain C names come back unchanged.
func TrimArgs(fn string) string {
	s := strings.TrimSpace(fn)
	for {
		t := s
		for _, q := range []string{" const", " volatile", " &&", " &"} {
			t = strings.TrimSuffix(t, q)
		}
		if t == s {
			break
		}
		s = t
	}
	if !strings.HasSuffix(s, ")") {
		return s
	}
	depth := 0
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				head := strings.TrimSpace(s[:i])
				// "operator()" with no argument list after it.
				if head == "" || strings.HasSuffix(head, "operator") {
					return s
				}
				return head
			}
		}
	}
	return s
}

// finish fills in the signature.
func finish(c *result.Crash) *result.Crash {
	var parts []string
	for _, f := range ProjectFrames(c) {
		if len(parts) == 3 {
			break
		}
		parts = append(parts, TrimArgs(f.Function))
	}
	if len(parts) == 0 && len(c.Frames) > 0 && c.Frames[0].File != "" {
		// No function names, for example UBSan without a stack trace.
		parts = append(parts, path.Base(c.Frames[0].File))
	}
	c.SignatureText = c.Kind + "|" + strings.Join(parts, "|")
	sum := sha256.Sum256([]byte(c.SignatureText))
	c.Signature = hex.EncodeToString(sum[:])[:16]
	return c
}
