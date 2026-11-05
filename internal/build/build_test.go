package build

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/sandbox"
	"github.com/DCchoudhury15/reprobot/internal/source"
	"github.com/DCchoudhury15/reprobot/internal/spec"
	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

const testImage = "gcc:15"

// asanBuild is the asan profile build line from project.yaml.tmpl.
const asanBuild = `make CFLAGS="-fsanitize=address -g -O1 -fno-omit-frame-pointer"`

func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", testImage).Run(); err != nil {
		t.Skipf("docker or image %s not usable: %v", testImage, err)
	}
}

// tinyparseProject is project.yaml.tmpl written as Go.
func tinyparseProject(t *testing.T, repo string) *spec.Project {
	t.Helper()
	tmpl, err := os.ReadFile(filepath.Join(testutil.FixtureDir(), "project.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{asanBuild, "image: gcc:15", "ASAN_OPTIONS: detect_leaks=0:symbolize=1"} {
		if !strings.Contains(string(tmpl), s) {
			t.Fatalf("project.yaml.tmpl no longer contains %q; update this test", s)
		}
	}
	return &spec.Project{
		Version: 1,
		ID:      "tinyparse",
		Repo:    repo,
		Image:   testImage,
		Profiles: map[string]spec.Profile{
			"asan": {
				Build:   asanBuild,
				Outputs: []string{"tinyparse"},
				Env:     map[string]string{"ASAN_OPTIONS": "detect_leaks=0:symbolize=1"},
			},
		},
		Limits: spec.Limits{CPUs: 1, MemoryMB: 1024, BuildTimeoutSeconds: 300},
	}
}

// countingBuilder returns a Builder whose sandbox runs are counted.
func countingBuilder(t *testing.T) (*Builder, *atomic.Int32) {
	var n atomic.Int32
	b := &Builder{CacheDir: t.TempDir()}
	b.run = func(ctx context.Context, s sandbox.Spec) (sandbox.Outcome, error) {
		n.Add(1)
		return sandbox.Run(ctx, s)
	}
	return b, &n
}

func resolve(t *testing.T, cache, repo, ref string) (mirror, sha string) {
	t.Helper()
	mirror, sha, err := source.Resolve(context.Background(), cache, repo, ref)
	if err != nil {
		t.Fatal(err)
	}
	return mirror, sha
}

// runTinyparse runs a built tinyparse on the fixture input, the way the
// runner does: outputs and reporter files read-only, unprivileged user.
func runTinyparse(t *testing.T, binDir string) sandbox.Outcome {
	t.Helper()
	out, err := sandbox.Run(context.Background(), sandbox.Spec{
		Image: testImage,
		Cmd:   []string{"bin/tinyparse", "poc/input.bin"},
		User:  "65534:65534",
		Env:   map[string]string{"ASAN_OPTIONS": "detect_leaks=0"},
		Mounts: []sandbox.Mount{
			{Source: binDir, Target: "/work/bin", ReadOnly: true},
			{Source: filepath.Join(testutil.FixtureDir(), "repro"), Target: "/work/poc", ReadOnly: true},
		},
		WorkDir:      "/work",
		ReadOnlyRoot: true,
		MemoryMB:     1024,
		Timeout:      30 * time.Second,
	})
	if err != nil {
		t.Fatalf("run tinyparse: %v", err)
	}
	return out
}

func TestBuildAndRunTinyparse(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	f := testutil.NewTinyparseRepo(t)
	b, runs := countingBuilder(t)
	p := tinyparseProject(t, f.RepoDir)
	mirror, vuln := resolve(t, b.CacheDir, f.RepoDir, f.Vuln)
	ctx := context.Background()

	// Two builders race for the same key: one builds, the other waits on
	// the lock and then finds the finished entry.
	var wg sync.WaitGroup
	outs := make([]Output, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = b.Build(ctx, p, "asan", vuln, mirror)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("build %d: %v\n%s", i, err, outs[i].LogTail)
		}
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("concurrent builds of one key ran %d containers, want 1", got)
	}
	if outs[0].CacheHit == outs[1].CacheHit {
		t.Fatalf("want exactly one cache hit, got %v and %v", outs[0].CacheHit, outs[1].CacheHit)
	}
	if outs[0].Key != outs[1].Key || outs[0].BinDir != outs[1].BinDir {
		t.Fatalf("outputs differ: %+v vs %+v", outs[0], outs[1])
	}
	vulnOut := outs[0]
	if vulnOut.Runtime != "runc" && vulnOut.Runtime != "runsc" {
		t.Errorf("Runtime = %q", vulnOut.Runtime)
	}
	if !strings.HasPrefix(vulnOut.ImageDigest, "sha256:") {
		t.Errorf("ImageDigest = %q", vulnOut.ImageDigest)
	}
	if want := filepath.Join(b.CacheDir, "builds", vulnOut.Key, "bin"); vulnOut.BinDir != want {
		t.Errorf("BinDir = %s, want %s", vulnOut.BinDir, want)
	}
	fi, err := os.Stat(filepath.Join(vulnOut.BinDir, "tinyparse"))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("bin/tinyparse mode %v, want an executable file", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(b.CacheDir, "builds", vulnOut.Key, "done")); err != nil {
		t.Errorf("done marker: %v", err)
	}
	assertNoTempEntries(t, b.CacheDir)

	// A later build of the same key is a cache hit with no container.
	again, err := b.Build(ctx, p, "asan", vuln, mirror)
	if err != nil {
		t.Fatal(err)
	}
	if !again.CacheHit || again.BinDir != vulnOut.BinDir || runs.Load() != 1 {
		t.Fatalf("second build: hit=%v bin=%s runs=%d", again.CacheHit, again.BinDir, runs.Load())
	}

	// The fix commit is a different key and a real build.
	_, fix := resolve(t, b.CacheDir, f.RepoDir, f.Fix)
	fixOut, err := b.Build(ctx, p, "asan", fix, mirror)
	if err != nil {
		t.Fatalf("build fix: %v\n%s", err, fixOut.LogTail)
	}
	if fixOut.CacheHit || fixOut.Key == vulnOut.Key || runs.Load() != 2 {
		t.Fatalf("fix build: hit=%v key=%s runs=%d", fixOut.CacheHit, fixOut.Key, runs.Load())
	}

	// End to end: the vulnerable build crashes under ASan, the fixed one
	// rejects the input cleanly.
	res := runTinyparse(t, vulnOut.BinDir)
	if !strings.Contains(res.Stderr, "AddressSanitizer: heap-buffer-overflow") {
		t.Fatalf("vuln run: exit %d, no ASan report in stderr:\n%s", res.ExitCode, res.Stderr)
	}
	if res.ExitCode == 0 {
		t.Errorf("vuln run exit code 0")
	}
	res = runTinyparse(t, fixOut.BinDir)
	if res.ExitCode != 1 || res.TimedOut || res.OOMKilled {
		t.Fatalf("fix run: %+v", res)
	}
	if strings.Contains(res.Stderr, "Sanitizer") || !strings.Contains(res.Stderr, "tinyparse: bad input") {
		t.Fatalf("fix run stderr:\n%s", res.Stderr)
	}
}

func assertNoTempEntries(t *testing.T, cacheDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(cacheDir, "builds"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp entry %s", e.Name())
		}
	}
}

// buildProfile builds the fixture Vuln commit with a custom profile.
func buildProfile(t *testing.T, b *Builder, prof spec.Profile) (Output, error) {
	t.Helper()
	f := testutil.NewTinyparseRepo(t)
	p := tinyparseProject(t, f.RepoDir)
	p.Profiles["test"] = prof
	mirror, sha := resolve(t, b.CacheDir, f.RepoDir, f.Vuln)
	return b.Build(context.Background(), p, "test", sha, mirror)
}

func TestBuildFailureIsNotCached(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	b, runs := countingBuilder(t)
	prof := spec.Profile{Build: "echo compiling; echo fatal-marker >&2; exit 3", Outputs: []string{"tinyparse"}}
	for i := 1; i <= 2; i++ {
		out, err := buildProfile(t, b, prof)
		if !errors.Is(err, ErrBuildFailed) {
			t.Fatalf("attempt %d: err = %v, want ErrBuildFailed", i, err)
		}
		if !strings.Contains(err.Error(), "exit code 3") {
			t.Errorf("err = %v, want exit code 3", err)
		}
		if !strings.Contains(out.LogTail, "compiling") || !strings.Contains(out.LogTail, "fatal-marker") {
			t.Errorf("LogTail = %q", out.LogTail)
		}
		if out.CacheHit || out.BinDir != "" {
			t.Errorf("failed build: %+v", out)
		}
		if _, err := os.Stat(filepath.Join(b.CacheDir, "builds", out.Key)); err == nil {
			t.Errorf("failed build left a cache entry")
		}
		if got := runs.Load(); got != int32(i) {
			t.Fatalf("attempt %d ran %d containers; a failure must not be cached", i, got)
		}
	}
	assertNoTempEntries(t, b.CacheDir)
}

func TestBuildHasNoNetwork(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	b, _ := countingBuilder(t)
	out, err := buildProfile(t, b, spec.Profile{
		Build:   "getent hosts example.org || exit 9; touch out",
		Outputs: []string{"out"},
	})
	if !errors.Is(err, ErrBuildFailed) || !strings.Contains(err.Error(), "exit code 9") {
		t.Fatalf("err = %v, want build failure with exit code 9 (log %q)", err, out.LogTail)
	}
}

func TestBuildMissingOutput(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	b, _ := countingBuilder(t)
	_, err := buildProfile(t, b, spec.Profile{Build: "true", Outputs: []string{"tinyparse"}})
	if !errors.Is(err, ErrBuildFailed) || !strings.Contains(err.Error(), "not built") {
		t.Fatalf("err = %v, want missing output failure", err)
	}
}

func TestBuildOutputSymlinks(t *testing.T) {
	requireDocker(t)
	t.Parallel()

	// A symlink to a host file must not be copied into the cache.
	b, _ := countingBuilder(t)
	_, err := buildProfile(t, b, spec.Profile{Build: "ln -s /etc/passwd evil", Outputs: []string{"evil"}})
	if !errors.Is(err, ErrBuildFailed) || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("err = %v, want outside-tree failure", err)
	}
	assertNoTempEntries(t, b.CacheDir)

	// A symlink to a file inside the tree is fine and is copied as a file.
	b, _ = countingBuilder(t)
	out, err := buildProfile(t, b, spec.Profile{
		Build:   "mkdir -p sub && printf hi > sub/real && chmod 755 sub/real && ln -s sub/real link",
		Outputs: []string{"link", "sub/real"},
	})
	if err != nil {
		t.Fatalf("inner symlink: %v\n%s", err, out.LogTail)
	}
	for _, name := range []string{"link", "real"} {
		p := filepath.Join(out.BinDir, name)
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s mode %v, want a 0755 regular file", name, fi.Mode())
		}
		if data, _ := os.ReadFile(p); string(data) != "hi" {
			t.Errorf("%s = %q", name, data)
		}
	}
}

func TestBuildConfigErrors(t *testing.T) {
	b := &Builder{CacheDir: t.TempDir()}
	b.run = func(context.Context, sandbox.Spec) (sandbox.Outcome, error) {
		t.Fatal("sandbox must not run for a bad config")
		return sandbox.Outcome{}, nil
	}
	p := tinyparseProject(t, "unused")
	sha := strings.Repeat("a", 40)
	if _, err := b.Build(context.Background(), p, "nosuch", sha, "unused"); err == nil {
		t.Error("unknown profile: nil error")
	}
	for _, outputs := range [][]string{nil, {"/abs"}, {"../up"}, {"a/../../up"}, {"x/tool", "y/tool"}, {"."}} {
		p.Profiles["bad"] = spec.Profile{Build: "true", Outputs: outputs}
		_, err := b.Build(context.Background(), p, "bad", sha, "unused")
		if err == nil || errors.Is(err, ErrBuildFailed) {
			t.Errorf("outputs %q: err = %v, want a config error", outputs, err)
		}
	}
}

func TestLogTail(t *testing.T) {
	out := "first-line\n" + strings.Repeat("o", 5000)
	got := logTail(out, "last-error\n")
	if len(got) != 4096 || !strings.HasSuffix(got, "o\nlast-error\n") || strings.Contains(got, "first-line") {
		t.Fatalf("len %d, tail %q", len(got), got[max(0, len(got)-40):])
	}
	if got := logTail("a", "b"); got != "a\nb" {
		t.Fatalf("short = %q", got)
	}
}

func TestBuildSpec(t *testing.T) {
	p := &spec.Project{Image: "img"}
	prof := spec.Profile{Build: "make", Env: map[string]string{"CC": "clang"}}
	s := buildSpec(p, prof, "/x/src")
	if s.Network || !s.ReadOnlyRoot || s.TmpfsMB != 512 || s.WorkDir != "/src" {
		t.Errorf("spec = %+v", s)
	}
	if s.CPUs != 2 || s.MemoryMB != 2048 || s.Timeout != 900*time.Second {
		t.Errorf("defaults: cpus %v mem %d timeout %v", s.CPUs, s.MemoryMB, s.Timeout)
	}
	if s.Env["HOME"] != "/tmp" || s.Env["CC"] != "clang" {
		t.Errorf("env = %v", s.Env)
	}
	if len(s.Mounts) != 1 || s.Mounts[0] != (sandbox.Mount{Source: "/x/src", Target: "/src"}) {
		t.Errorf("mounts = %v", s.Mounts)
	}
	if want := strings.Join([]string{"sh", "-c", "make"}, " "); strings.Join(s.Cmd, " ") != want {
		t.Errorf("cmd = %q", s.Cmd)
	}
	p.Limits = spec.Limits{CPUs: 1.5, MemoryMB: 512, BuildTimeoutSeconds: 60}
	s = buildSpec(p, prof, "/x/src")
	if s.CPUs != 1.5 || s.MemoryMB != 512 || s.Timeout != time.Minute {
		t.Errorf("limits: cpus %v mem %d timeout %v", s.CPUs, s.MemoryMB, s.Timeout)
	}
}

func TestKey(t *testing.T) {
	base := spec.Profile{
		Build:   asanBuild,
		Outputs: []string{"tinyparse", "helper"},
		Env:     map[string]string{"ASAN_OPTIONS": "detect_leaks=0", "CC": "gcc", "Z": "1"},
	}
	key := func(id, sha, name string, prof spec.Profile, digest string) string {
		return Key(id, sha, name, prof, digest)
	}
	k := key("tinyparse", "aaa", "asan", base, "sha256:1")
	if len(k) != 64 {
		t.Fatalf("key %q is not sha256 hex", k)
	}
	// Deterministic, and map order does not matter.
	for range 20 {
		env := map[string]string{}
		for kk, v := range base.Env {
			env[kk] = v
		}
		p := base
		p.Env = env
		if got := key("tinyparse", "aaa", "asan", p, "sha256:1"); got != k {
			t.Fatalf("key changed between calls: %s vs %s", got, k)
		}
	}
	// Output order does not matter.
	reordered := base
	reordered.Outputs = []string{"helper", "tinyparse"}
	if key("tinyparse", "aaa", "asan", reordered, "sha256:1") != k {
		t.Error("output order changed the key")
	}

	withEnv := func(env map[string]string) spec.Profile { p := base; p.Env = env; return p }
	withBuild := func(b string) spec.Profile { p := base; p.Build = b; return p }
	withOutputs := func(o ...string) spec.Profile { p := base; p.Outputs = o; return p }
	changed := map[string]string{
		"project":      key("other", "aaa", "asan", base, "sha256:1"),
		"commit":       key("tinyparse", "bbb", "asan", base, "sha256:1"),
		"profile name": key("tinyparse", "aaa", "ubsan", base, "sha256:1"),
		"build":        key("tinyparse", "aaa", "asan", withBuild(asanBuild+" -j2"), "sha256:1"),
		"outputs":      key("tinyparse", "aaa", "asan", withOutputs("tinyparse"), "sha256:1"),
		"env value":    key("tinyparse", "aaa", "asan", withEnv(map[string]string{"ASAN_OPTIONS": "detect_leaks=1", "CC": "gcc", "Z": "1"}), "sha256:1"),
		"env name":     key("tinyparse", "aaa", "asan", withEnv(map[string]string{"ASAN_OPTIONS": "detect_leaks=0", "CXX": "gcc", "Z": "1"}), "sha256:1"),
		"env removed":  key("tinyparse", "aaa", "asan", withEnv(map[string]string{"ASAN_OPTIONS": "detect_leaks=0", "CC": "gcc"}), "sha256:1"),
		"image":        key("tinyparse", "aaa", "asan", base, "sha256:2"),
	}
	seen := map[string]string{k: "base"}
	for what, got := range changed {
		if prev, ok := seen[got]; ok {
			t.Errorf("changing %s gives the same key as %s", what, prev)
		}
		seen[got] = what
	}

	// Length prefixes keep field boundaries apart.
	a := Key("ab", "c", "asan", base, "d")
	b := Key("a", "bc", "asan", base, "d")
	if a == b {
		t.Error("project/commit boundary is ambiguous")
	}
	e1 := Key("p", "s", "n", withEnv(map[string]string{"A": "B=C"}), "d")
	e2 := Key("p", "s", "n", withEnv(map[string]string{"A=B": "C"}), "d")
	if e1 == e2 {
		t.Error("env name/value boundary is ambiguous")
	}
	o1 := Key("p", "s", "n", withOutputs("a", "b"), "d")
	o2 := Key("p", "s", "n", withOutputs("a\x00b"), "d")
	o3 := Key("p", "s", "n", withOutputs("ab"), "d")
	if o1 == o2 || o1 == o3 || o2 == o3 {
		t.Error("output list encoding is ambiguous")
	}
}
