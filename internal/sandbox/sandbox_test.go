package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testImage = "gcc:15"

// requireDocker skips the test when docker or the test image is missing.
// It never pulls.
func requireDocker(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", testImage).Run(); err != nil {
		t.Skipf("docker or image %s not usable: %v", testImage, err)
	}
}

func run(t *testing.T, s Spec) Outcome {
	t.Helper()
	if s.Image == "" {
		s.Image = testImage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := Run(ctx, s)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, out.Stderr)
	}
	assertRemoved(t, out.Container)
	return out
}

// assertRemoved checks that no container with this name exists.
func assertRemoved(t *testing.T, name string) {
	t.Helper()
	if !strings.HasPrefix(name, "reprobot-") {
		t.Fatalf("bad container name %q", name)
	}
	b, err := exec.Command("docker", "ps", "-a", "-q", "--filter", "name=^/"+name+"$").CombinedOutput()
	if err != nil {
		t.Fatalf("docker ps: %v: %s", err, b)
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		t.Fatalf("container %s still exists: %s", name, s)
	}
}

func TestRunExitCode(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"sh", "-c", "exit 7"}})
	if out.ExitCode != 7 || out.TimedOut || out.OOMKilled {
		t.Fatalf("got %+v, want exit 7", out)
	}
	if out.Duration <= 0 {
		t.Fatalf("duration not set: %v", out.Duration)
	}
}

func TestRunCapturesOutput(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"sh", "-c", "echo to-out; echo to-err >&2"}})
	if out.Stdout != "to-out\n" {
		t.Errorf("stdout = %q", out.Stdout)
	}
	if out.Stderr != "to-err\n" {
		t.Errorf("stderr = %q", out.Stderr)
	}
}

func TestRunOutputLimits(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	// Each stream gets about 1.9 MB.
	out := run(t, Spec{Cmd: []string{"sh", "-c", "seq 1 300000; seq 1 300000 >&2"}})
	if len(out.Stdout) != MaxStdout || !strings.HasPrefix(out.Stdout, "1\n2\n3\n") {
		t.Errorf("stdout: len %d, head %q; want first %d bytes", len(out.Stdout), out.Stdout[:min(10, len(out.Stdout))], MaxStdout)
	}
	if len(out.Stderr) != MaxStderr || !strings.HasSuffix(out.Stderr, "\n299999\n300000\n") {
		t.Errorf("stderr: len %d; want last %d bytes", len(out.Stderr), MaxStderr)
	}
}

func TestRunEnvAndWorkDir(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{
		Cmd:     []string{"sh", "-c", `echo "$A $B"; pwd`},
		Env:     map[string]string{"A": "one", "B": "two=2"},
		WorkDir: "/work",
	})
	if out.Stdout != "one two=2\n/work\n" {
		t.Fatalf("stdout = %q", out.Stdout)
	}
}

func netInterfaces(procNetDev string) []string {
	var names []string
	for _, line := range strings.Split(procNetDev, "\n") {
		if name, _, ok := strings.Cut(line, ":"); ok {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

func TestRunNoNetwork(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"cat", "/proc/net/dev"}})
	if got := netInterfaces(out.Stdout); !slices.Equal(got, []string{"lo"}) {
		t.Fatalf("interfaces = %v, want only lo", got)
	}
	// Control: with Network set the container has more than lo, so the
	// check above can tell the difference.
	out = run(t, Spec{Cmd: []string{"cat", "/proc/net/dev"}, Network: true})
	if got := netInterfaces(out.Stdout); len(got) < 2 {
		t.Fatalf("with network, interfaces = %v", got)
	}
}

func TestRunReadOnlyRoot(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	script := `touch /x 2>/dev/null && echo root-writable; touch /tmp/y && echo tmp-writable`
	out := run(t, Spec{Cmd: []string{"sh", "-c", script}, ReadOnlyRoot: true})
	if strings.Contains(out.Stdout, "root-writable") {
		t.Errorf("root is writable: %q", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "tmp-writable") {
		t.Errorf("/tmp is not writable: %q %q", out.Stdout, out.Stderr)
	}
}

func TestRunReadOnlyMount(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "in"), []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `cat /m/in; touch /m/new 2>/dev/null && echo mount-writable; exit 0`
	out := run(t, Spec{
		Cmd:    []string{"sh", "-c", script},
		Mounts: []Mount{{Source: dir, Target: "/m", ReadOnly: true}},
	})
	if out.Stdout != "data\n" {
		t.Fatalf("stdout = %q, want only the file content", out.Stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err == nil {
		t.Fatal("container wrote into a read-only mount")
	}
}

func TestRunTimeout(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	start := time.Now()
	out := run(t, Spec{Cmd: []string{"sleep", "60"}, Timeout: 2 * time.Second})
	if el := time.Since(start); el > 20*time.Second {
		t.Fatalf("Run took %v with a 2s timeout", el)
	}
	if !out.TimedOut {
		t.Fatalf("TimedOut false: %+v", out)
	}
}

func TestRunNoTimeoutWhenFast(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"true"}, Timeout: 30 * time.Second})
	if out.TimedOut || out.ExitCode != 0 {
		t.Fatalf("got %+v", out)
	}
}

func TestRunOOM(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	hog := `x=$(head -c 200000000 /dev/zero | tr "\0" a); echo ${#x}`
	out := run(t, Spec{Cmd: []string{"sh", "-c", hog}, MemoryMB: 64, Timeout: time.Minute})
	if !out.OOMKilled {
		t.Fatalf("OOMKilled false: exit %d stdout %q", out.ExitCode, out.Stdout)
	}
}

func TestRunUser(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"sh", "-c", "id -u; id -g"}, User: "65534:65534"})
	if out.Stdout != "65534\n65534\n" {
		t.Fatalf("stdout = %q", out.Stdout)
	}
}

func statusField(status, key string) string {
	for _, line := range strings.Split(status, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func TestRunHardening(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	// Default user is root, which shows the capability drop best.
	script := `cat /proc/self/status; echo ==; cat /sys/fs/cgroup/pids.max /sys/fs/cgroup/memory.max /sys/fs/cgroup/memory.swap.max`
	out := run(t, Spec{Cmd: []string{"sh", "-c", script}, MemoryMB: 128})
	status, cg, ok := strings.Cut(out.Stdout, "==\n")
	if !ok {
		t.Fatalf("stdout = %q stderr = %q", out.Stdout, out.Stderr)
	}
	for _, k := range []string{"CapEff", "CapPrm", "CapBnd"} {
		if v := statusField(status, k); v != "0000000000000000" {
			t.Errorf("%s = %q, want all zeros", k, v)
		}
	}
	if v := statusField(status, "NoNewPrivs"); v != "1" {
		t.Errorf("NoNewPrivs = %q, want 1", v)
	}
	want := []string{"256", "134217728", "0"}
	if got := strings.Fields(cg); !slices.Equal(got, want) {
		t.Errorf("pids.max, memory.max, memory.swap.max = %v, want %v", got, want)
	}
}

func TestRunCommandNotFound(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	out := run(t, Spec{Cmd: []string{"/no/such/cmd"}})
	if out.ExitCode != 127 || out.StartError == "" {
		t.Fatalf("got exit %d start error %q", out.ExitCode, out.StartError)
	}
}

func TestRunContextCancelRemovesContainer(t *testing.T) {
	requireDocker(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	out, err := Run(ctx, Spec{Image: testImage, Cmd: []string{"sleep", "60"}})
	if err == nil {
		t.Fatalf("Run returned no error after cancel: %+v", out)
	}
	if el := time.Since(start); el > 40*time.Second {
		t.Fatalf("Run took %v after cancel", el)
	}
	assertRemoved(t, out.Container)
}

func TestRuntime(t *testing.T) {
	requireDocker(t)
	rt, err := Runtime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command("docker", "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "runc"
	if bytes.Contains(b, []byte(`"runsc"`)) {
		want = "runsc"
	}
	if rt != want {
		t.Fatalf("Runtime = %q, want %q", rt, want)
	}
}

func TestParseRuntimes(t *testing.T) {
	for in, want := range map[string]string{
		`{"runc":{"path":"runc"},"io.containerd.runc.v2":{}}`:        "runc",
		`{"runc":{"path":"runc"},"runsc":{"path":"/usr/bin/runsc"}}`: "runsc",
		`{"my-runsc-wrapper":{"path":"x"}}`:                          "runc",
	} {
		got, err := parseRuntimes([]byte(in + "\n"))
		if err != nil || got != want {
			t.Errorf("parseRuntimes(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseRuntimes([]byte("not json")); err == nil {
		t.Error("want error for bad input")
	}
}

func TestCreateArgs(t *testing.T) {
	dir := t.TempDir()
	args, err := createArgs(Spec{
		Image:  "img",
		Cmd:    []string{"--not-a-flag"},
		Env:    map[string]string{"Z": "1", "A": "2", "M": "3"},
		Mounts: []Mount{{Source: dir, Target: "/m", ReadOnly: true}, {Source: dir, Target: "/w"}},
	}, "reprobot-x", "runsc")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--network=none", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--pids-limit=256", "--memory=1024m", "--memory-swap=1024m", "--cpus=1",
		"--tmpfs=/tmp:rw,nosuid,nodev,size=64m", "--runtime=runsc",
		"--volume=" + dir + ":/m:ro", "--volume=" + dir + ":/w",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %s in %v", want, args)
		}
	}
	if slices.Contains(args, "--read-only") {
		t.Error("--read-only set without ReadOnlyRoot")
	}
	// Env must be sorted, and the command must come after the image.
	var env []string
	for _, a := range args {
		if strings.HasPrefix(a, "--env=") {
			env = append(env, a)
		}
	}
	if !slices.Equal(env, []string{"--env=A=2", "--env=M=3", "--env=Z=1"}) {
		t.Errorf("env = %v", env)
	}
	if n := len(args); args[n-2] != "img" || args[n-1] != "--not-a-flag" {
		t.Errorf("tail = %v", args[n-2:])
	}

	bad := []Spec{
		{Image: "", Cmd: []string{"x"}},
		{Image: "--privileged", Cmd: []string{"x"}},
		{Image: "img"},
		{Image: "img", Cmd: []string{"x"}, Env: map[string]string{"A=B": "c"}},
		{Image: "img", Cmd: []string{"x"}, WorkDir: "rel"},
		{Image: "img", Cmd: []string{"x"}, Mounts: []Mount{{Source: "vol", Target: "/m"}}},
		{Image: "img", Cmd: []string{"x"}, Mounts: []Mount{{Source: dir + ":/etc", Target: "/m"}}},
		{Image: "img", Cmd: []string{"x"}, Mounts: []Mount{{Source: filepath.Join(dir, "missing"), Target: "/m"}}},
	}
	for _, s := range bad {
		if _, err := createArgs(s, "reprobot-x", "runc"); err == nil {
			t.Errorf("createArgs(%+v) = nil error", s)
		}
	}
}

func TestHeadBuffer(t *testing.T) {
	h := &headBuffer{limit: 10}
	for _, s := range []string{"abc", "defgh", "ijklmn", "op"} {
		if n, err := h.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if got := h.String(); got != "abcdefghij" {
		t.Fatalf("got %q", got)
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{limit: 10}
	var all strings.Builder
	for i := 0; i < 100; i++ {
		s := strings.Repeat(string(rune('a'+i%26)), i%7)
		all.WriteString(s)
		if n, err := tb.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
		want := all.String()
		if len(want) > 10 {
			want = want[len(want)-10:]
		}
		if got := tb.String(); got != want {
			t.Fatalf("after %d writes got %q, want %q", i+1, got, want)
		}
	}
	tb.Write([]byte("0123456789ABCDEF"))
	if got := tb.String(); got != "6789ABCDEF" {
		t.Fatalf("big write: got %q", got)
	}
}

func TestPickDigest(t *testing.T) {
	cases := []struct {
		image   string
		digests []string
		want    string
	}{
		{"gcc:15", []string{"gcc@sha256:aa"}, "sha256:aa"},
		{"gcc:15", []string{"mirror.example/gcc@sha256:bb", "gcc@sha256:aa"}, "sha256:aa"},
		{"docker.io/library/gcc:15", []string{"gcc@sha256:aa"}, "sha256:aa"},
		{"localhost:5000/x:1", []string{"other@sha256:cc", "localhost:5000/x@sha256:dd"}, "sha256:dd"},
		{"local:dev", nil, "sha256:id"},
	}
	for _, c := range cases {
		if got := pickDigest(c.image, c.digests, "sha256:id"); got != c.want {
			t.Errorf("pickDigest(%s, %v) = %s, want %s", c.image, c.digests, got, c.want)
		}
	}
}

func TestImageDigest(t *testing.T) {
	requireDocker(t)
	got, err := ImageDigest(context.Background(), testImage)
	if err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command("docker", "image", "inspect", "--format", "{{json .RepoDigests}}", testImage).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("digest %q is not a sha256 digest", got)
	}
	if !bytes.Contains(b, []byte("@"+got)) {
		t.Fatalf("digest %s not in RepoDigests %s", got, b)
	}
}
