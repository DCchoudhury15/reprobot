package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/build"
	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sandbox"
	"github.com/DCchoudhury15/reprobot/internal/source"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

func crashOf(kind, sig string) *result.Crash {
	return &result.Crash{Sanitizer: "address", Kind: kind, Signature: sig}
}

// kindCompare matches when the crash kind equals the claimed kind and
// records which crash it was given.
type kindCompare struct{ got []*result.Crash }

func (k *kindCompare) fn(c spec.Claim, crash *result.Crash) (bool, []string) {
	k.got = append(k.got, crash)
	if crash.Kind == c.Kind {
		return true, nil
	}
	return false, []string{fmt.Sprintf("claimed %s, got %s", c.Kind, crash.Kind)}
}

func TestDecide(t *testing.T) {
	claim := spec.Claim{Sanitizer: "address", Kind: "heap-buffer-overflow", Frame: "parse_header"}
	hbo := crashOf("heap-buffer-overflow", "A")
	hbo2 := crashOf("heap-buffer-overflow", "A")
	sbo := crashOf("stack-buffer-overflow", "B")
	sbo2 := crashOf("stack-buffer-overflow", "B")
	crash := func(c *result.Crash) result.RunOutcome { return result.RunOutcome{ExitCode: 1, Crash: c} }
	clean := result.RunOutcome{}
	timeout := result.RunOutcome{TimedOut: true, ExitCode: -1}
	oom := result.RunOutcome{OOMKilled: true, ExitCode: 137}

	tests := []struct {
		name         string
		runs         []result.RunOutcome
		state        result.State
		crash        *result.Crash
		reason       string
		findings     []string
		compareCalls int
	}{
		{
			name:   "zero runs",
			state:  result.NotReproduced,
			reason: "the proof of concept was not run (0 runs)",
		},
		{
			name:         "all crashed as claimed",
			runs:         []result.RunOutcome{crash(hbo), crash(hbo2), crash(hbo2)},
			state:        result.ReproducedAsClaimed,
			crash:        hbo,
			reason:       "crashed in 3 of 3 runs, as claimed",
			compareCalls: 1,
		},
		{
			name:         "all crashed, other kind",
			runs:         []result.RunOutcome{crash(sbo), crash(sbo2)},
			state:        result.ReproducedDifferent,
			crash:        sbo,
			reason:       "crashed in 2 of 2 runs, but not as claimed",
			findings:     []string{"claimed heap-buffer-overflow, got stack-buffer-overflow"},
			compareCalls: 1,
		},
		{
			name:         "mixed signatures pick the majority",
			runs:         []result.RunOutcome{crash(sbo), crash(hbo), crash(hbo2)},
			state:        result.ReproducedAsClaimed,
			crash:        hbo,
			reason:       "crashed in 3 of 3 runs, as claimed; 2 different crash signatures, the most common one was seen in 2 runs",
			compareCalls: 1,
		},
		{
			name:         "mixed signatures, majority is not as claimed",
			runs:         []result.RunOutcome{crash(hbo), crash(sbo), crash(sbo2)},
			state:        result.ReproducedDifferent,
			crash:        sbo,
			reason:       "crashed in 3 of 3 runs, but not as claimed; 2 different crash signatures, the most common one was seen in 2 runs",
			findings:     []string{"claimed heap-buffer-overflow, got stack-buffer-overflow"},
			compareCalls: 1,
		},
		{
			name:         "tie goes to the first signature",
			runs:         []result.RunOutcome{crash(sbo), crash(hbo)},
			state:        result.ReproducedDifferent,
			crash:        sbo,
			reason:       "crashed in 2 of 2 runs, but not as claimed; 2 different crash signatures, the most common one was seen in 1 run",
			findings:     []string{"claimed heap-buffer-overflow, got stack-buffer-overflow"},
			compareCalls: 1,
		},
		{
			name:         "some runs crashed",
			runs:         []result.RunOutcome{clean, crash(hbo), clean},
			state:        result.Flaky,
			crash:        hbo,
			reason:       "crashed in 1 of 3 runs",
			compareCalls: 1,
		},
		{
			name:         "flaky keeps comparison findings",
			runs:         []result.RunOutcome{crash(sbo), timeout, crash(sbo2), oom},
			state:        result.Flaky,
			crash:        sbo,
			reason:       "crashed in 2 of 4 runs",
			findings:     []string{"claimed heap-buffer-overflow, got stack-buffer-overflow"},
			compareCalls: 1,
		},
		{
			name:   "timeout before oom",
			runs:   []result.RunOutcome{clean, oom, timeout},
			state:  result.Timeout,
			reason: "1 of 3 runs hit the time limit",
		},
		{
			name:   "all timed out",
			runs:   []result.RunOutcome{timeout, timeout},
			state:  result.Timeout,
			reason: "2 of 2 runs hit the time limit",
		},
		{
			name:   "oom before not reproduced",
			runs:   []result.RunOutcome{clean, oom, oom},
			state:  result.OOM,
			reason: "2 of 3 runs hit the memory limit",
		},
		{
			name:   "no crash",
			runs:   []result.RunOutcome{clean, {ExitCode: 1}, clean},
			state:  result.NotReproduced,
			reason: "no crash in 3 runs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp := &kindCompare{}
			state, crash, reason, findings := Decide(claim, tt.runs, cmp.fn)
			if state != tt.state {
				t.Errorf("state = %s, want %s", state, tt.state)
			}
			if crash != tt.crash {
				t.Errorf("crash = %+v, want %+v", crash, tt.crash)
			}
			if reason != tt.reason {
				t.Errorf("reason = %q, want %q", reason, tt.reason)
			}
			if !reflect.DeepEqual(findings, tt.findings) {
				t.Errorf("findings = %q, want %q", findings, tt.findings)
			}
			if len(cmp.got) != tt.compareCalls {
				t.Fatalf("compare called %d times, want %d", len(cmp.got), tt.compareCalls)
			}
			if tt.compareCalls > 0 && cmp.got[0] != tt.crash {
				t.Errorf("compare got crash %+v, want the representative %+v", cmp.got[0], tt.crash)
			}
		})
	}
}

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// fake stands in for every package Run calls.
type fake struct {
	t         *testing.T
	repro     *spec.Reproducer
	project   *spec.Project
	reproPath string
	cacheDir  string
	binDir    string
	buildOut  build.Output
	outcomes  []sandbox.Outcome // one per run
	reports   []string          // sanitizer log file each run writes ("" = none)

	loadReproErr, loadProjectErr, checkErr, filePathsErr, hashErr error
	filePathsOverride                                             []string
	resolveErr, claimCheckErr, buildErr, runtimeErr, runErr       error

	calls    []string
	specs    []sandbox.Spec
	pocFiles []map[string]string // poc dir contents seen by each run
	outDirs  []string
}

func newFake(t *testing.T) *fake {
	t.Helper()
	reproDir := filepath.Join(t.TempDir(), "repro")
	if err := os.MkdirAll(filepath.Join(reproDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"input.bin": "AAAA", "sub/extra.txt": "extra"} {
		if err := os.WriteFile(filepath.Join(reproDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	crash := sandbox.Outcome{ExitCode: 77, Stderr: "program stderr", Duration: time.Second}
	report := "CRASH heap-buffer-overflow"
	return &fake{
		t: t,
		repro: &spec.Reproducer{
			Version: 1, Project: "tinyparse", Commit: "v1.0", Profile: "asan",
			Run:   spec.Run{Cmd: []string{"bin/tinyparse", "poc/input.bin"}, TimeoutSeconds: 7, Repeat: 3},
			Files: []string{"input.bin", "sub/extra.txt"},
			Claim: spec.Claim{Sanitizer: "address", Kind: "heap-buffer-overflow", Frame: "parse_header"},
		},
		project: &spec.Project{
			Version: 1, ID: "tinyparse", Repo: "/repo/tinyparse", Image: "gcc:15",
			Profiles: map[string]spec.Profile{"asan": {
				Build: "make", Outputs: []string{"tinyparse"},
				Env: map[string]string{"ASAN_OPTIONS": "detect_leaks=0"},
			}},
		},
		reproPath: filepath.Join(reproDir, "repro.yaml"),
		cacheDir:  t.TempDir(),
		binDir:    t.TempDir(),
		buildOut:  build.Output{Key: "key1", CacheHit: true, LogTail: "build log", ImageDigest: "sha256:img", Runtime: "runc"},
		outcomes:  []sandbox.Outcome{crash, crash, crash},
		reports:   []string{report, report, report},
	}
}

func (f *fake) deps() deps {
	t := f.t
	return deps{
		loadReproducer: func(path string) (*spec.Reproducer, error) {
			f.calls = append(f.calls, "loadReproducer")
			if path != f.reproPath {
				t.Errorf("loadReproducer(%q)", path)
			}
			return f.repro, f.loadReproErr
		},
		loadProject: func(path string) (*spec.Project, error) {
			f.calls = append(f.calls, "loadProject")
			return f.project, f.loadProjectErr
		},
		checkAgainst: func(r *spec.Reproducer, p *spec.Project) error {
			f.calls = append(f.calls, "checkAgainst")
			return f.checkErr
		},
		filePaths: func(r *spec.Reproducer, baseDir string) ([]string, error) {
			f.calls = append(f.calls, "filePaths")
			if baseDir != filepath.Dir(f.reproPath) {
				t.Errorf("filePaths base dir %q", baseDir)
			}
			if f.filePathsOverride != nil {
				return f.filePathsOverride, f.filePathsErr
			}
			var out []string
			for _, name := range r.Files {
				out = append(out, filepath.Join(baseDir, name))
			}
			return out, f.filePathsErr
		},
		reproducerHash: func(reproPath string, r *spec.Reproducer) (string, error) {
			f.calls = append(f.calls, "reproducerHash")
			return "hash1", f.hashErr
		},
		resolve: func(ctx context.Context, cacheDir, repo, ref string) (string, string, error) {
			f.calls = append(f.calls, "resolve")
			if cacheDir != f.cacheDir || repo != "/repo/tinyparse" || ref != "v1.0" {
				t.Errorf("resolve(%q, %q, %q)", cacheDir, repo, ref)
			}
			if f.resolveErr != nil {
				return "", "", f.resolveErr
			}
			return "/mirror", testSHA, nil
		},
		claimCheck: func(ctx context.Context, mirrorDir, sha string, c spec.Claim) ([]string, error) {
			f.calls = append(f.calls, "claimCheck")
			if mirrorDir != "/mirror" || sha != testSHA || c != f.repro.Claim {
				t.Errorf("claimCheck(%q, %q, %+v)", mirrorDir, sha, c)
			}
			return []string{"static finding"}, f.claimCheckErr
		},
		build: func(ctx context.Context, cacheDir string, p *spec.Project, profile, sha, mirrorDir string) (build.Output, error) {
			f.calls = append(f.calls, "build")
			if cacheDir != f.cacheDir || p != f.project || profile != "asan" || sha != testSHA || mirrorDir != "/mirror" {
				t.Errorf("build(%q, %p, %q, %q, %q)", cacheDir, p, profile, sha, mirrorDir)
			}
			out := f.buildOut
			out.BinDir = f.binDir
			return out, f.buildErr
		},
		runtime: func(ctx context.Context) (string, error) {
			f.calls = append(f.calls, "runtime")
			return "runsc", f.runtimeErr
		},
		sandboxRun: func(ctx context.Context, s sandbox.Spec) (sandbox.Outcome, error) {
			f.calls = append(f.calls, "sandboxRun")
			f.specs = append(f.specs, s)
			f.pocFiles = append(f.pocFiles, readPocDir(t, s))
			if f.runErr != nil {
				return sandbox.Outcome{}, f.runErr
			}
			i := len(f.specs) - 1
			out := mountSource(t, s, "/work/out")
			f.outDirs = append(f.outDirs, out)
			// Act like the sanitizer: write the report to the secret log dir.
			if i < len(f.reports) && f.reports[i] != "" {
				if err := os.WriteFile(filepath.Join(out, "tok", "asan.1"), []byte(f.reports[i]), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			return f.outcomes[i], nil
		},
		parse: func(stderr string) *result.Crash {
			if kind, ok := strings.CutPrefix(stderr, "CRASH "); ok {
				return crashOf(kind, "sig-"+kind)
			}
			return nil
		},
		compare: func(c spec.Claim, crash *result.Crash) (bool, []string) {
			return crash.Kind == c.Kind, []string{"compare finding"}
		},
		secret: func() (string, int, error) { return "tok", 77, nil },
	}
}

func mountSource(t *testing.T, s sandbox.Spec, target string) string {
	t.Helper()
	for _, m := range s.Mounts {
		if m.Target == target {
			return m.Source
		}
	}
	t.Fatalf("no %s mount in %+v", target, s.Mounts)
	return ""
}

// readPocDir returns the files in the poc mount (relative name to
// content) and checks they are readable by an unprivileged uid.
func readPocDir(t *testing.T, s sandbox.Spec) map[string]string {
	t.Helper()
	var dir string
	for _, m := range s.Mounts {
		if m.Target == "/work/poc" {
			dir = m.Source
		}
	}
	if dir == "" {
		t.Error("no /work/poc mount")
		return nil
	}
	files := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0o644)
		if info.IsDir() {
			want = 0o755
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", p, info.Mode().Perm(), want)
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	return files
}

func (f *fake) run(t *testing.T) *result.Result {
	t.Helper()
	res, err := run(context.Background(), Options{ProjectPath: "/p/project.yaml", ReproPath: f.reproPath, CacheDir: f.cacheDir}, f.deps())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func (f *fake) called(name string) int {
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

func TestRunReproduced(t *testing.T) {
	f := newFake(t)
	before := time.Now()
	res := f.run(t)

	if res.State != result.ReproducedAsClaimed || res.Reason != "crashed in 3 of 3 runs, as claimed" {
		t.Errorf("got %s %q", res.State, res.Reason)
	}
	wantInputs := result.Inputs{
		Project: "tinyparse", Commit: testSHA, Profile: "asan", ImageDigest: "sha256:img",
		ReproducerHash: "hash1", BuildCacheKey: "key1", SandboxRuntime: "runsc",
	}
	if res.Inputs != wantInputs {
		t.Errorf("inputs = %+v\nwant     %+v", res.Inputs, wantInputs)
	}
	if !res.BuildCacheHit || res.Crashed != 3 || len(res.Runs) != 3 {
		t.Errorf("cache hit %v, crashed %d, runs %d", res.BuildCacheHit, res.Crashed, len(res.Runs))
	}
	if res.Crash == nil || res.Crash.Signature != "sig-heap-buffer-overflow" {
		t.Errorf("crash = %+v", res.Crash)
	}
	if want := []string{"static finding", "compare finding"}; !reflect.DeepEqual(res.ClaimFindings, want) {
		t.Errorf("claim findings = %q, want %q", res.ClaimFindings, want)
	}
	if res.StartedAt.Before(before.Add(-time.Second)) || res.FinishedAt.Before(res.StartedAt) {
		t.Errorf("times: started %v finished %v", res.StartedAt, res.FinishedAt)
	}
	for i, r := range res.Runs {
		if r.ExitCode != 77 || r.Duration != time.Second || r.StderrTail != "program stderr" || r.Report != "CRASH heap-buffer-overflow" || r.Crash == nil || r.Note != "" {
			t.Errorf("run %d = %+v", i, r)
		}
	}

	if len(f.specs) != 3 {
		t.Fatalf("%d sandbox runs, want 3", len(f.specs))
	}
	pocDir := f.specs[0].Mounts[1].Source
	want := sandbox.Spec{
		Image: "gcc:15",
		Cmd:   []string{"bin/tinyparse", "poc/input.bin"},
		User:  "65534:65534",
		Env: map[string]string{
			"ASAN_OPTIONS":  "detect_leaks=0:log_path=/work/out/tok/asan:exitcode=77:abort_on_error=0",
			"UBSAN_OPTIONS": "print_stacktrace=1:log_path=/work/out/tok/ubsan:exitcode=77:abort_on_error=0",
		},
		WorkDir: "/work",
		Mounts: []sandbox.Mount{
			{Source: f.binDir, Target: "/work/bin", ReadOnly: true},
			{Source: pocDir, Target: "/work/poc", ReadOnly: true},
			{Target: "/work/out"},
		},
		Network:      false,
		CPUs:         2,
		MemoryMB:     2048,
		PidsLimit:    256,
		Timeout:      7 * time.Second,
		ReadOnlyRoot: true,
		TmpfsMB:      64,
	}
	for i, s := range f.specs {
		want.Mounts[2].Source = f.outDirs[i]
		if !reflect.DeepEqual(s, want) {
			t.Errorf("run %d spec:\n got %+v\nwant %+v", i, s, want)
		}
	}
	wantFiles := map[string]string{"input.bin": "AAAA", filepath.Join("sub", "extra.txt"): "extra"}
	for i, files := range f.pocFiles {
		if !reflect.DeepEqual(files, wantFiles) {
			t.Errorf("run %d poc files = %v, want %v", i, files, wantFiles)
		}
	}
	if !strings.HasPrefix(pocDir, filepath.Join(f.cacheDir, "tmp")+string(filepath.Separator)) {
		t.Errorf("poc dir %q is not under the cache dir", pocDir)
	}
	if _, err := os.Stat(pocDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("poc dir still exists after Run: %v", err)
	}
	if f.project.Profiles["asan"].Env["UBSAN_OPTIONS"] != "" || f.project.Profiles["asan"].Env["ASAN_OPTIONS"] != "detect_leaks=0" {
		t.Error("Run changed the profile env map")
	}
	seen := map[string]bool{}
	for _, d := range f.outDirs {
		if seen[d] {
			t.Errorf("out dir %q reused across runs", d)
		}
		seen[d] = true
		if !strings.HasPrefix(d, filepath.Join(f.cacheDir, "tmp")+string(filepath.Separator)) {
			t.Errorf("out dir %q is not under the cache dir", d)
		}
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("out dir still exists after Run: %v", err)
		}
	}
}

func TestRunOutcomesAreCopied(t *testing.T) {
	f := newFake(t)
	f.outcomes = []sandbox.Outcome{
		{ExitCode: -1, TimedOut: true},
		{ExitCode: 137, OOMKilled: true},
		{ExitCode: 0, Stderr: strings.Repeat("x", 10000) + "END"},
	}
	f.reports = nil
	res := f.run(t)
	if res.State != result.Timeout || res.Reason != "1 of 3 runs hit the time limit" {
		t.Errorf("got %s %q", res.State, res.Reason)
	}
	if !res.Runs[0].TimedOut || res.Runs[0].ExitCode != -1 || !res.Runs[1].OOMKilled || res.Runs[1].ExitCode != 137 {
		t.Errorf("runs = %+v", res.Runs[:2])
	}
	tail := res.Runs[2].StderrTail
	if len(tail) != 8192 || !strings.HasSuffix(tail, "xEND") {
		t.Errorf("stderr tail has %d bytes, ends %q", len(tail), tail[max(0, len(tail)-4):])
	}
	if res.Crashed != 0 || res.Crash != nil {
		t.Errorf("crashed %d, crash %+v", res.Crashed, res.Crash)
	}
	if want := []string{"static finding"}; !reflect.DeepEqual(res.ClaimFindings, want) {
		t.Errorf("claim findings = %q, want %q", res.ClaimFindings, want)
	}
}

func TestRunStopsEarly(t *testing.T) {
	bad := errors.New("bad input file")
	tests := []struct {
		name    string
		setup   func(f *fake)
		state   result.State
		reason  string
		lastRun string // last dependency that must be called
	}{
		{"repro does not load", func(f *fake) { f.loadReproErr = bad }, result.InvalidInput, "bad input file", "loadReproducer"},
		{"project does not load", func(f *fake) { f.loadProjectErr = bad }, result.InvalidInput, "bad input file", "loadProject"},
		{"repro does not fit project", func(f *fake) { f.checkErr = bad }, result.InvalidInput, "bad input file", "checkAgainst"},
		{"profile missing", func(f *fake) { f.repro.Profile = "msan" }, result.InvalidInput, `project "tinyparse" has no profile "msan"`, "checkAgainst"},
		{"file escapes", func(f *fake) { f.filePathsErr = bad }, result.InvalidInput, "bad input file", "filePaths"},
		{"file missing", func(f *fake) {
			f.filePathsOverride = []string{filepath.Join(filepath.Dir(f.reproPath), "nope"), filepath.Join(filepath.Dir(f.reproPath), "sub")}
		}, result.InvalidInput, "", "filePaths"},
		{"file is a dir", func(f *fake) {
			f.filePathsOverride = []string{filepath.Join(filepath.Dir(f.reproPath), "input.bin"), filepath.Join(filepath.Dir(f.reproPath), "sub")}
		}, result.InvalidInput, `file "sub/extra.txt" is not a regular file`, "filePaths"},
		{"hash fails", func(f *fake) { f.hashErr = bad }, result.InvalidInput, "bad input file", "reproducerHash"},
		{"claim is not a crash", func(f *fake) { f.repro.Claim.Sanitizer = "none" }, result.OutOfScope,
			"the claim is not a crash (claim.sanitizer is none), so running it cannot test it", "reproducerHash"},
		{"unknown commit", func(f *fake) { f.resolveErr = fmt.Errorf("resolve v1.0: %w", source.ErrUnknownRef) }, result.InvalidInput,
			`commit "v1.0" does not exist in the project repo`, "resolve"},
		{"build fails", func(f *fake) { f.buildErr = fmt.Errorf("make: %w", build.ErrBuildFailed) }, result.BuildFailed,
			`profile "asan" did not build at 0123456`, "build"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			tt.setup(f)
			res := f.run(t)
			if res.State != tt.state {
				t.Errorf("state = %s, want %s (reason %q)", res.State, tt.state, res.Reason)
			}
			if tt.reason != "" && res.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", res.Reason, tt.reason)
			}
			if res.Reason == "" {
				t.Error("empty reason")
			}
			if got := f.calls[len(f.calls)-1]; got != tt.lastRun {
				t.Errorf("last call = %s, want %s (calls %v)", got, tt.lastRun, f.calls)
			}
			if f.called("sandboxRun") != 0 || len(res.Runs) != 0 {
				t.Error("the proof of concept ran")
			}
			if res.StartedAt.IsZero() || res.FinishedAt.IsZero() {
				t.Error("times not set")
			}
		})
	}
}

func TestRunBuildFailedKeepsDetails(t *testing.T) {
	f := newFake(t)
	f.buildErr = build.ErrBuildFailed
	f.buildOut.CacheHit = false
	res := f.run(t)
	if res.State != result.BuildFailed || res.BuildLogTail != "build log" {
		t.Errorf("state %s, log tail %q", res.State, res.BuildLogTail)
	}
	if res.Inputs.Commit != testSHA || res.Inputs.BuildCacheKey != "key1" || res.Inputs.ImageDigest != "sha256:img" || res.Inputs.SandboxRuntime != "runc" {
		t.Errorf("inputs = %+v", res.Inputs)
	}
	if want := []string{"static finding"}; !reflect.DeepEqual(res.ClaimFindings, want) {
		t.Errorf("claim findings = %q, want %q", res.ClaimFindings, want)
	}
}

func TestRunInfrastructureErrors(t *testing.T) {
	infra := errors.New("docker is down")
	tests := []struct {
		name  string
		setup func(f *fake)
	}{
		{"resolve", func(f *fake) { f.resolveErr = infra }},
		{"claim check", func(f *fake) { f.claimCheckErr = infra }},
		{"build", func(f *fake) { f.buildErr = infra }},
		{"runtime", func(f *fake) { f.runtimeErr = infra }},
		{"sandbox", func(f *fake) { f.runErr = infra }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			tt.setup(f)
			res, err := run(context.Background(), Options{ProjectPath: "/p", ReproPath: f.reproPath, CacheDir: f.cacheDir}, f.deps())
			if !errors.Is(err, infra) || res != nil {
				t.Fatalf("got %+v, %v; want nil and the infrastructure error", res, err)
			}
			if f.called("sandboxRun") > 1 {
				t.Error("kept running after an error")
			}
			// No poc dir may be left behind.
			entries, _ := os.ReadDir(filepath.Join(f.cacheDir, "tmp"))
			if len(entries) != 0 {
				t.Errorf("left %d entries in the cache tmp dir", len(entries))
			}
		})
	}
	f := newFake(t)
	if _, err := run(context.Background(), Options{ReproPath: f.reproPath}, f.deps()); err == nil {
		t.Error("empty cache dir: want an error")
	}
}

func TestRunSpecLimitsAndDefaults(t *testing.T) {
	p := &spec.Project{
		Image: "img",
		Profiles: map[string]spec.Profile{
			"ubsan": {Env: map[string]string{"UBSAN_OPTIONS": "halt_on_error=1"}},
		},
		Limits: spec.Limits{CPUs: 0.5, MemoryMB: 512},
	}
	r := &spec.Reproducer{Profile: "ubsan", Run: spec.Run{Cmd: []string{"x"}}}
	s := runSpec(p, r, "/bin", "/poc")
	if s.CPUs != 0.5 || s.MemoryMB != 512 {
		t.Errorf("limits: cpus %v, memory %d", s.CPUs, s.MemoryMB)
	}
	if s.Env["UBSAN_OPTIONS"] != "halt_on_error=1" || len(s.Env) != 1 {
		t.Errorf("profile UBSAN_OPTIONS must win: %v", s.Env)
	}
	if s.Timeout != 30*time.Second {
		t.Errorf("default timeout = %v", s.Timeout)
	}
	r.Run.TimeoutSeconds = 10000
	if s := runSpec(p, r, "/bin", "/poc"); s.Timeout != 300*time.Second {
		t.Errorf("capped timeout = %v", s.Timeout)
	}
	for in, want := range map[int]int{0: 5, -1: 5, 1: 1, 20: 20, 21: 20} {
		if got := repeat(in); got != want {
			t.Errorf("repeat(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestRunRepeatCount(t *testing.T) {
	f := newFake(t)
	f.repro.Run.Repeat = 0
	crash := sandbox.Outcome{ExitCode: 1, Stderr: "CRASH heap-buffer-overflow"}
	f.outcomes = []sandbox.Outcome{crash, crash, crash, crash, crash}
	res := f.run(t)
	if len(f.specs) != spec.DefaultRepeat || len(res.Runs) != spec.DefaultRepeat {
		t.Errorf("%d runs, want %d", len(f.specs), spec.DefaultRepeat)
	}
}

func TestPocDirRejectsEscapingNames(t *testing.T) {
	src := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../f", "/etc/f", "a/../../f", ""} {
		dir, err := makePocDir(t.TempDir(), []string{name}, []string{src})
		if err == nil {
			t.Errorf("name %q: want an error", name)
		}
		if dir != "" {
			os.RemoveAll(dir)
		}
	}
}

func TestTail(t *testing.T) {
	if got := tail("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := tail("abcdef", 3); got != "def" {
		t.Errorf("got %q", got)
	}
	// "é" is two bytes; a cut in its middle must drop the broken half.
	if got := tail("aé", 1); got != "" {
		t.Errorf("got %q", got)
	}
	if got := tail("xxéz", 3); got != "éz" {
		t.Errorf("got %q", got)
	}
	if got := tail("xxéz", 2); got != "z" {
		t.Errorf("got %q", got)
	}
}

// A sanitizer report on stderr is not a crash: the reporter controls the
// input and the program may echo it.
func TestRunIgnoresReportOnStderr(t *testing.T) {
	f := newFake(t)
	f.reports = nil
	echo := sandbox.Outcome{ExitCode: 77, Stderr: "CRASH heap-buffer-overflow"}
	f.outcomes = []sandbox.Outcome{echo, echo, echo}
	res := f.run(t)
	if res.State != result.NotReproduced || res.Crashed != 0 || res.Crash != nil {
		t.Errorf("got %s, crashed %d, crash %+v", res.State, res.Crashed, res.Crash)
	}
}

// A report file only counts with the secret exit code.
func TestRunIgnoresReportWithWrongExitCode(t *testing.T) {
	f := newFake(t)
	other := sandbox.Outcome{ExitCode: 1}
	f.outcomes = []sandbox.Outcome{other, other, other}
	res := f.run(t)
	if res.State != result.NotReproduced || res.Crashed != 0 {
		t.Errorf("got %s, crashed %d", res.State, res.Crashed)
	}
	for i, r := range res.Runs {
		if !strings.Contains(r.Note, "exit code was 1") || r.Report != "" {
			t.Errorf("run %d: note %q, report %q", i, r.Note, r.Report)
		}
	}
}

func TestRunRejectsCommandsTheProjectDoesNotBuild(t *testing.T) {
	for _, cmd := range [][]string{
		{"sh", "-c", "cat poc/report.txt >&2"},
		{"/bin/sh"},
		{"bin/../bin/tinyparse"},
		{"bin/other"},
		{"poc/input.bin"},
	} {
		f := newFake(t)
		f.repro.Run.Cmd = cmd
		res := f.run(t)
		if res.State != result.InvalidInput || f.called("sandboxRun") != 0 || f.called("build") != 0 {
			t.Errorf("cmd %q: got %s %q, %d runs", cmd, res.State, res.Reason, f.called("sandboxRun"))
		}
	}
	for _, first := range []string{"bin/tinyparse", "./bin/tinyparse", "/work/bin/tinyparse"} {
		if err := checkCmd([]string{first, "poc/input.bin"}, []string{"build/out/tinyparse"}); err != nil {
			t.Errorf("%q: %v", first, err)
		}
	}
}

func TestMakeOutDirModes(t *testing.T) {
	cache := t.TempDir()
	dir, err := makeOutDir(cache, "tok")
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o711 {
		t.Errorf("out dir mode %v (%v), want 0711 so the run user cannot list it", st.Mode().Perm(), err)
	}
	st, err = os.Stat(filepath.Join(dir, "tok"))
	if err != nil || st.Mode().Perm() != 0o733 || st.Mode()&os.ModeSetgid == 0 {
		t.Errorf("log dir mode %v (%v), want 0733 with setgid", st.Mode(), err)
	}
}

// The run user owns the log files and could plant a symlink to a host file
// or a FIFO. Neither may be read or block.
func TestCollectReportSkipsSymlinksAndFifos(t *testing.T) {
	parse := func(s string) *result.Crash {
		if kind, ok := strings.CutPrefix(s, "CRASH "); ok {
			return crashOf(kind, "sig")
		}
		return nil
	}
	dir, err := makeOutDir(t.TempDir(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "host-file")
	if err := os.WriteFile(host, []byte("CRASH heap-buffer-overflow"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(host, filepath.Join(dir, "tok", "asan.1")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "tok", "asan.2"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var crash *result.Crash
	var report, note string
	go func() {
		defer close(done)
		crash, report, note, err = collectReport(dir, "tok", 77, 77, parse)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collectReport blocked on a FIFO")
	}
	if err != nil || crash != nil || report != "" {
		t.Errorf("crash %+v, report %q, err %v", crash, report, err)
	}
	if strings.Count(note, "skipped") != 2 {
		t.Errorf("note = %q, want both files skipped", note)
	}
}

func TestWithReportChannelOptionsComeLast(t *testing.T) {
	s := sandbox.Spec{
		Env:    map[string]string{"ASAN_OPTIONS": "log_path=/tmp/x:exitcode=1:abort_on_error=1:"},
		Mounts: make([]sandbox.Mount, 1, 4),
	}
	got := withReportChannel(s, "/out", "tok", 99)
	if want := "log_path=/tmp/x:exitcode=1:abort_on_error=1:log_path=/work/out/tok/asan:exitcode=99:abort_on_error=0"; got.Env["ASAN_OPTIONS"] != want {
		t.Errorf("ASAN_OPTIONS = %q\nwant %q", got.Env["ASAN_OPTIONS"], want)
	}
	if got.Env["UBSAN_OPTIONS"] != "log_path=/work/out/tok/ubsan:exitcode=99:abort_on_error=0" {
		t.Errorf("UBSAN_OPTIONS = %q", got.Env["UBSAN_OPTIONS"])
	}
	if len(s.Env) != 1 || len(s.Mounts) != 1 {
		t.Error("withReportChannel changed the shared spec")
	}
	m := got.Mounts[len(got.Mounts)-1]
	if m.Source != "/out" || m.Target != "/work/out" || m.ReadOnly {
		t.Errorf("out mount = %+v", m)
	}
}

func TestNewSecret(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		tok, code, err := newSecret()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 32 || strings.Trim(tok, "0123456789abcdef") != "" || seen[tok] {
			t.Errorf("token %q", tok)
		}
		seen[tok] = true
		if code < 64 || code > 120 {
			t.Errorf("exit code %d outside 64..120", code)
		}
	}
}
