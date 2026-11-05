// Package testutil builds the tinyparse fixture used by tests.
//
// The fixture is a git repo with three commits:
//
//	Vuln:   parse_header copies up to 255 bytes into a 16 byte heap buffer
//	Fix:    adds a length check
//	Revert: reverts the fix, so the bug is back
//
// A correct reprobot must say REPRODUCED_AS_CLAIMED for Vuln and Revert and
// NOT_REPRODUCED for Fix.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tinyparse holds the fixture repo and its commits.
type Tinyparse struct {
	RepoDir string
	Vuln    string
	Fix     string
	Revert  string
}

// FixtureDir returns testdata/fixtures/tinyparse in this source tree.
func FixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures", "tinyparse")
}

// NewTinyparseRepo creates the fixture git repo in a temp dir.
func NewTinyparseRepo(t testing.TB) Tinyparse {
	t.Helper()
	src := FixtureDir()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.org",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.org",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	copyFile(t, filepath.Join(src, "Makefile"), filepath.Join(dir, "Makefile"))
	copyFile(t, filepath.Join(src, "vuln", "tinyparse.c"), filepath.Join(dir, "tinyparse.c"))
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "vulnerable parser")
	vuln := git("rev-parse", "HEAD")
	copyFile(t, filepath.Join(src, "fixed", "tinyparse.c"), filepath.Join(dir, "tinyparse.c"))
	git("commit", "-q", "-am", "check name length")
	fix := git("rev-parse", "HEAD")
	git("revert", "--no-edit", "HEAD")
	revert := git("rev-parse", "HEAD")
	return Tinyparse{RepoDir: dir, Vuln: vuln, Fix: fix, Revert: revert}
}

// WriteInputs writes project.yaml and a repro dir (repro.yaml + input.bin)
// for the given commit into a temp dir and returns their paths.
func WriteInputs(t testing.TB, repoDir, commit string) (projectPath, reproPath string) {
	t.Helper()
	src := FixtureDir()
	dir := t.TempDir()
	projectPath = filepath.Join(dir, "project.yaml")
	writeTemplate(t, filepath.Join(src, "project.yaml.tmpl"), projectPath, "{{REPO}}", repoDir)
	reproDir := filepath.Join(dir, "repro")
	if err := os.MkdirAll(reproDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reproPath = filepath.Join(reproDir, "repro.yaml")
	writeTemplate(t, filepath.Join(src, "repro", "repro.yaml.tmpl"), reproPath, "{{COMMIT}}", commit)
	copyFile(t, filepath.Join(src, "repro", "input.bin"), filepath.Join(reproDir, "input.bin"))
	return projectPath, reproPath
}

func writeTemplate(t testing.TB, src, dst, key, value string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(strings.ReplaceAll(string(b), key, value)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t testing.TB, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
