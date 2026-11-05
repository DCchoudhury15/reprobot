//go:build e2e

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

// These tests need Docker and the gcc:15 image.

// edit replaces old with new in the file at path. old must be there.
func edit(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// check runs reprobot on the fixture at commit. change, when set, edits
// the written project.yaml and repro.yaml first.
func check(t *testing.T, repoDir, cacheDir, commit string, change func(projectPath, reproPath string)) *result.Result {
	t.Helper()
	projectPath, reproPath := testutil.WriteInputs(t, repoDir, commit)
	if change != nil {
		change(projectPath, reproPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := Run(ctx, Options{ProjectPath: projectPath, ReproPath: reproPath, CacheDir: cacheDir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	t.Logf("%s: %s", res.State, res.Reason)
	return res
}

func wantState(t *testing.T, res *result.Result, want result.State) {
	t.Helper()
	if res.State != want {
		t.Fatalf("state = %s (%s), want %s\nbuild log: %s\nruns: %+v", res.State, res.Reason, want, res.BuildLogTail, res.Runs)
	}
}

func TestE2EVulnFixRevert(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()

	vuln := check(t, f.RepoDir, cache, f.Vuln, nil)
	wantState(t, vuln, result.ReproducedAsClaimed)
	if vuln.Crash == nil || vuln.Crash.Signature == "" || vuln.Crash.Kind != "heap-buffer-overflow" {
		t.Fatalf("crash = %+v", vuln.Crash)
	}
	if vuln.Crashed != 3 || len(vuln.Runs) != 3 {
		t.Errorf("crashed %d of %d runs, want 3 of 3", vuln.Crashed, len(vuln.Runs))
	}
	in := vuln.Inputs
	if in.Project != "tinyparse" || in.Commit != f.Vuln || in.Profile != "asan" ||
		in.ImageDigest == "" || in.ReproducerHash == "" || in.BuildCacheKey == "" ||
		(in.SandboxRuntime != "runc" && in.SandboxRuntime != "runsc") {
		t.Errorf("inputs = %+v", in)
	}
	if len(vuln.ClaimFindings) != 0 {
		t.Errorf("the fixture claim is accurate, got findings %q", vuln.ClaimFindings)
	}
	if !strings.Contains(vuln.Runs[0].Report, "AddressSanitizer: heap-buffer-overflow") {
		t.Errorf("run does not keep the sanitizer report: %q", vuln.Runs[0].Report)
	}

	fix := check(t, f.RepoDir, cache, f.Fix, nil)
	wantState(t, fix, result.NotReproduced)
	if fix.Crash != nil || fix.Crashed != 0 {
		t.Errorf("fix crashed: %+v", fix.Crash)
	}

	revert := check(t, f.RepoDir, cache, f.Revert, nil)
	wantState(t, revert, result.ReproducedAsClaimed)
	if revert.Crash == nil || revert.Crash.Signature != vuln.Crash.Signature {
		t.Errorf("revert signature %+v, want %q", revert.Crash, vuln.Crash.Signature)
	}

	again := check(t, f.RepoDir, cache, f.Vuln, nil)
	wantState(t, again, result.ReproducedAsClaimed)
	if !again.BuildCacheHit || again.Inputs.BuildCacheKey != vuln.Inputs.BuildCacheKey {
		t.Errorf("second check of Vuln: cache hit %v, key %q, first key %q", again.BuildCacheHit, again.Inputs.BuildCacheKey, vuln.Inputs.BuildCacheKey)
	}
	if again.Crash.Signature != vuln.Crash.Signature {
		t.Errorf("signature changed between checks: %q then %q", vuln.Crash.Signature, again.Crash.Signature)
	}
}

func TestE2EOtherKind(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Vuln, func(_, repro string) {
		edit(t, repro, "kind: heap-buffer-overflow", "kind: stack-buffer-overflow")
	})
	wantState(t, res, result.ReproducedDifferent)
	if res.Crash == nil || res.Crash.Kind != "heap-buffer-overflow" {
		t.Errorf("crash = %+v", res.Crash)
	}
	if len(res.ClaimFindings) == 0 {
		t.Error("want a finding that explains the mismatch")
	}
}

func TestE2EInventedFunctionIsAFinding(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Vuln, func(_, repro string) {
		edit(t, repro, "16 byte heap buffer.", "16 byte heap buffer, as `parse_blob` shows.")
	})
	wantState(t, res, result.ReproducedAsClaimed)
	want := `function "parse_blob" does not appear anywhere in the code at ` + f.Vuln[:7]
	if len(res.ClaimFindings) != 1 || res.ClaimFindings[0] != want {
		t.Errorf("claim findings = %q, want [%q]", res.ClaimFindings, want)
	}
}

func TestE2EOutOfScope(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Vuln, func(_, repro string) {
		edit(t, repro, "sanitizer: address", "sanitizer: none")
	})
	wantState(t, res, result.OutOfScope)
	if len(res.Runs) != 0 || res.Inputs.BuildCacheKey != "" {
		t.Errorf("out of scope claim was built or run: %+v", res)
	}
}

func TestE2EUnknownCommit(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), "deadbeef", nil)
	wantState(t, res, result.InvalidInput)
	if !strings.Contains(res.Reason, "deadbeef") {
		t.Errorf("reason does not name the commit: %q", res.Reason)
	}
}

func TestE2EBuildFailed(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Vuln, func(project, _ string) {
		edit(t, project, `build: make CFLAGS="-fsanitize=address -g -O1 -fno-omit-frame-pointer"`, "build: exit 3")
	})
	wantState(t, res, result.BuildFailed)
	if len(res.Runs) != 0 {
		t.Errorf("ran %d times after a failed build", len(res.Runs))
	}
}

// extraProfiles are maintainer profiles used by the tests below. echoer
// stands for a real program that prints bad input in its error messages.
const extraProfiles = `  echo:
    build: |
      printf '#!/bin/sh\ncat "$1" >&2\nexit 1\n' > echoer && chmod 755 echoer
    outputs: [echoer]
  hang:
    build: |
      printf '#!/bin/sh\nexec sleep 100\n' > hang && chmod 755 hang
    outputs: [hang]
limits:`

// fakeReport writes a real captured ASan report for this bug next to the
// repro.yaml and lists it as a reporter file.
func fakeReport(t *testing.T, repro string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.FixtureDir(), "..", "..", "sanitizer", "out", "tinyparse.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "AddressSanitizer: heap-buffer-overflow") {
		t.Fatal("captured report is not an ASan report")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(repro), "report.txt"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	edit(t, repro, "  - input.bin", "  - input.bin\n  - report.txt")
}

// A program that echoes reporter input to stderr must not turn a pasted
// sanitizer report into a crash. The Fix commit has no bug at all.
func TestE2EFakeReportOnStderrIsIgnored(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Fix, func(project, repro string) {
		edit(t, project, "limits:", extraProfiles)
		fakeReport(t, repro)
		edit(t, repro, "profile: asan", "profile: echo")
		edit(t, repro, `cmd: ["bin/tinyparse", "poc/input.bin"]`, `cmd: ["bin/echoer", "poc/report.txt"]`)
	})
	wantState(t, res, result.NotReproduced)
	if !strings.Contains(res.Runs[0].StderrTail, "AddressSanitizer: heap-buffer-overflow") {
		t.Fatalf("the fake report did not reach stderr, so this test proves nothing: %q", res.Runs[0].StderrTail)
	}
}

// The reporter may only run programs the maintainers build. A shell would
// let the reporter print anything.
func TestE2EReporterShellIsRejected(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	res := check(t, f.RepoDir, t.TempDir(), f.Fix, func(_, repro string) {
		fakeReport(t, repro)
		edit(t, repro, `cmd: ["bin/tinyparse", "poc/input.bin"]`, `cmd: ["sh", "-c", "cat poc/report.txt >&2; exit 1"]`)
	})
	wantState(t, res, result.InvalidInput)
	if len(res.Runs) != 0 {
		t.Errorf("ran %d times", len(res.Runs))
	}
}

func TestE2ETimeout(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	start := time.Now()
	res := check(t, f.RepoDir, t.TempDir(), f.Vuln, func(project, repro string) {
		edit(t, project, "limits:", extraProfiles)
		edit(t, repro, "profile: asan", "profile: hang")
		edit(t, repro, `cmd: ["bin/tinyparse", "poc/input.bin"]`, `cmd: ["bin/hang"]`)
		edit(t, repro, "timeout_seconds: 20", "timeout_seconds: 2")
	})
	wantState(t, res, result.Timeout)
	if len(res.Runs) != 3 {
		t.Errorf("%d runs, want 3", len(res.Runs))
	}
	for i, r := range res.Runs {
		if !r.TimedOut {
			t.Errorf("run %d did not time out: %+v", i, r)
		}
	}
	// Without the limit the three runs alone would take 300 seconds.
	if d := time.Since(start); d > 3*time.Minute {
		t.Errorf("took %v; the 2 second limit was not enforced", d)
	}
}
