// Package claimcheck does the cheap static check: do the functions, files
// and file:line references in the claim exist at the reported commit?
package claimcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// MaxRefs is the most references one claim can make us check.
const MaxRefs = 50

// minIdentLen is the shortest identifier treated as a function name.
const minIdentLen = 3

// stoplist holds libc and common names that a report can mention without
// the project defining or calling them.
var stoplist = map[string]bool{
	"memcpy": true, "memmove": true, "memset": true, "malloc": true,
	"calloc": true, "realloc": true, "free": true, "strlen": true,
	"strcpy": true, "strncpy": true, "strcat": true, "sprintf": true,
	"snprintf": true, "printf": true, "fprintf": true, "fopen": true,
	"fread": true, "fwrite": true, "fclose": true, "main": true,
	"sizeof": true, "return": true, "if": true, "for": true, "while": true,
}

var (
	shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// fileRe matches a source path with an optional :line or :line:col.
	// The \b after the extension keeps "x.cs" from being read as "x.c".
	fileRe = regexp.MustCompile(`(/?(?:[\w.\-]+/)*[\w\-][\w.\-]*\.(?:cpp|cxx|cc|c|hpp|hh|h|go|rs|py|js|ts|java))\b(?::(\d+)(?::(\d+))?)?`)
	// backtickRe matches an identifier in backticks, like `name`,
	// `name()` or `ns::name`.
	backtickRe = regexp.MustCompile("`(?:[A-Za-z_]\\w*::)*([A-Za-z_]\\w*)(?:\\(\\))?`")
	// callRe matches an identifier directly followed by "(".
	callRe = regexp.MustCompile(`\b([A-Za-z_]\w*)\(`)
	// frameRe takes the function name at the start of claim.Frame.
	frameRe = regexp.MustCompile(`^\s*(?:[A-Za-z_]\w*::)*([A-Za-z_]\w*)`)
)

type refKind int

const (
	refFunc refKind = iota
	refFile
)

// ref is one code reference found in a claim.
type ref struct {
	kind refKind
	name string // function name or file path
	line int    // cited line, 0 when none
}

// Check looks for code references in the claim and returns one finding for
// each reference that does not exist at sha. mirrorDir is a git repo
// (bare is fine) that contains sha.
func Check(ctx context.Context, mirrorDir, sha string, c spec.Claim) ([]string, error) {
	if !shaRe.MatchString(sha) {
		return nil, fmt.Errorf("claimcheck: %q is not a full 40 character commit sha", sha)
	}
	refs := extract(c)
	if len(refs) == 0 {
		return nil, nil
	}
	short := sha[:7]
	var files []string
	var findings []string
	for _, r := range refs {
		switch r.kind {
		case refFunc:
			found, err := grepWord(ctx, mirrorDir, sha, r.name)
			if err != nil {
				return nil, err
			}
			if !found {
				findings = append(findings, fmt.Sprintf("function %q does not appear anywhere in the code at %s", r.name, short))
			}
		case refFile:
			if files == nil {
				var err error
				if files, err = listFiles(ctx, mirrorDir, sha); err != nil {
					return nil, err
				}
			}
			matches := matchPath(files, r.name)
			if len(matches) == 0 {
				findings = append(findings, fmt.Sprintf("file %q does not exist at %s", r.name, short))
				continue
			}
			if len(matches) > 1 || r.line == 0 {
				// More than one file fits: we cannot tell which one is meant.
				continue
			}
			n, err := countLines(ctx, mirrorDir, sha, matches[0])
			if err != nil {
				return nil, err
			}
			if r.line > n {
				findings = append(findings, fmt.Sprintf("%s has %s at %s, but the report cites line %d", matches[0], plural(n, "line"), short, r.line))
			}
		}
	}
	return findings, nil
}

// extract returns the unique references in c, functions first, at most
// MaxRefs of them.
func extract(c spec.Claim) []ref {
	var refs []ref
	seen := map[ref]bool{}
	add := func(r ref) {
		if len(refs) < MaxRefs && !seen[r] {
			seen[r] = true
			refs = append(refs, r)
		}
	}
	addFunc := func(name string) {
		if len(name) >= minIdentLen && !stoplist[name] {
			add(ref{kind: refFunc, name: name})
		}
	}

	if m := frameRe.FindStringSubmatch(c.Frame); m != nil {
		addFunc(m[1])
	}
	text := c.Frame + "\n" + c.Text
	for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
		addFunc(m[1])
	}
	for _, m := range callRe.FindAllStringSubmatch(text, -1) {
		addFunc(m[1])
	}
	for _, m := range fileRe.FindAllStringSubmatch(text, -1) {
		if runtimePath(m[1]) {
			continue
		}
		line := 0
		if m[2] != "" {
			if n, err := strconv.Atoi(m[2]); err == nil {
				line = n
			}
		}
		add(ref{kind: refFile, name: m[1], line: line})
	}
	return refs
}

// runtimePath reports whether p is a sanitizer runtime source file. Pasted
// sanitizer traces cite these, and they are not part of the project.
func runtimePath(p string) bool {
	return strings.Contains(p, "libsanitizer/") || strings.Contains(p, "compiler-rt/")
}

// matchPath returns the repo files that name refers to. A cited path
// matches a repo path when one ends with the other at a "/" boundary, so
// "parse.c", "src/parse.c" and "/build/proj/src/parse.c" all match
// "src/parse.c".
func matchPath(files []string, name string) []string {
	name = strings.TrimPrefix(name, "./")
	var out []string
	for _, f := range files {
		if f == name || strings.HasSuffix(f, "/"+name) || strings.HasSuffix(name, "/"+f) {
			out = append(out, f)
		}
	}
	return out
}

func listFiles(ctx context.Context, dir, sha string) ([]string, error) {
	out, err := git(ctx, dir, "ls-tree", "-r", "-z", "--name-only", sha)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

func countLines(ctx context.Context, dir, sha, path string) (int, error) {
	out, err := git(ctx, dir, "show", sha+":"+path)
	if err != nil {
		return 0, err
	}
	n := bytes.Count(out, []byte("\n"))
	if len(out) > 0 && out[len(out)-1] != '\n' {
		n++
	}
	return n, nil
}

// grepWord reports whether name appears as a whole word anywhere in the
// tree at sha.
func grepWord(ctx context.Context, dir, sha, name string) (bool, error) {
	_, err := git(ctx, dir, "grep", "-q", "-w", "-F", "-e", name, sha, "--")
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
		return false, nil
	}
	return false, err
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return out, nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("claimcheck: git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
