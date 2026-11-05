// Package sandbox runs one command in a fresh, locked-down container.
//
// It drives the docker CLI. Every run gets a new container that is always
// removed. Without gVisor (runsc) the container still runs with no network,
// no capabilities, no new privileges, a pid limit, a memory limit without
// swap, a CPU limit and (when asked) a read-only root.
package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mount is a bind mount into the container.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// Spec describes one sandboxed command.
type Spec struct {
	Image        string
	Cmd          []string
	User         string // "uid:gid"; empty means the image default
	Env          map[string]string
	WorkDir      string
	Mounts       []Mount
	Network      bool // false means no network at all
	CPUs         float64
	MemoryMB     int
	PidsLimit    int
	Timeout      time.Duration // zero means no limit other than ctx
	ReadOnlyRoot bool
	TmpfsMB      int // size of the /tmp tmpfs
}

// Outcome is what happened.
type Outcome struct {
	ExitCode  int
	Stdout    string // first MaxStdout bytes
	Stderr    string // last MaxStderr bytes
	TimedOut  bool
	OOMKilled bool
	Duration  time.Duration
	// StartError is set when the command could not be started at all, for
	// example when it does not exist. ExitCode is then 126 or 127.
	StartError string
	// Container is the name of the container used for this run. It is
	// already removed when Run returns.
	Container string
}

// Defaults used when a Spec field is zero.
const (
	DefaultTmpfsMB   = 64
	DefaultPidsLimit = 256
	DefaultMemoryMB  = 1024
	DefaultCPUs      = 1
)

// Output limits: the first MaxStdout bytes of stdout and the last MaxStderr
// bytes of stderr are kept. Sanitizer reports go to stderr and come last.
const (
	MaxStdout = 1 << 20
	MaxStderr = 1 << 20
)

// cleanupTimeout bounds docker kill and docker rm, which run even after ctx
// is cancelled.
const cleanupTimeout = 30 * time.Second

// Runtime returns "runsc" when the gVisor runtime is registered with
// Docker, otherwise "runc".
func Runtime(ctx context.Context) (string, error) {
	out, err := docker(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return "", err
	}
	return parseRuntimes(out)
}

func parseRuntimes(out []byte) (string, error) {
	var rts map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(out), &rts); err != nil {
		return "", fmt.Errorf("parse docker runtimes: %w", err)
	}
	if _, ok := rts["runsc"]; ok {
		return "runsc", nil
	}
	return "runc", nil
}

// Run starts one fresh container, waits for it, and always removes it.
func Run(ctx context.Context, s Spec) (Outcome, error) {
	rt, err := Runtime(ctx)
	if err != nil {
		return Outcome{}, err
	}
	name, err := containerName()
	if err != nil {
		return Outcome{}, err
	}
	args, err := createArgs(s, name, rt)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Container: name}

	// Remove the container on every path, also when ctx is cancelled.
	// docker rm -f on a name that was never created only returns an error.
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		_, _ = docker(cctx, "rm", "-f", name)
	}()

	if _, err := docker(ctx, args...); err != nil {
		return out, fmt.Errorf("create container: %w", err)
	}

	stdout := &headBuffer{limit: MaxStdout}
	stderr := &tailBuffer{limit: MaxStderr}
	cmd := exec.CommandContext(ctx, "docker", "start", "-a", name)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second

	begin := time.Now()
	if err := cmd.Start(); err != nil {
		return out, fmt.Errorf("start container: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var deadline <-chan time.Time
	if s.Timeout > 0 {
		t := time.NewTimer(s.Timeout)
		defer t.Stop()
		deadline = t.C
	}
	select {
	case <-done:
	case <-deadline:
		kctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		_, kerr := docker(kctx, "kill", name)
		cancel()
		// kill fails when the container already stopped on its own.
		out.TimedOut = kerr == nil
		<-done
	}
	out.Duration = time.Since(begin)
	out.Stdout = stdout.String()
	out.Stderr = stderr.String()

	if err := ctx.Err(); err != nil {
		return out, fmt.Errorf("run container: %w", err)
	}

	st, err := inspectState(ctx, name)
	if err != nil {
		return out, err
	}
	if st.Running {
		// docker start -a returned early; wait for the real exit.
		if _, err := docker(ctx, "wait", name); err != nil {
			return out, fmt.Errorf("wait container: %w", err)
		}
		if st, err = inspectState(ctx, name); err != nil {
			return out, err
		}
	}
	out.ExitCode = st.ExitCode
	out.OOMKilled = st.OOMKilled
	if st.Status == "created" {
		// The command never started (for example: not found).
		out.StartError = st.Error
		if out.StartError == "" {
			out.StartError = "container did not start"
		}
		if out.ExitCode == 0 {
			return out, fmt.Errorf("container did not start: %s", strings.TrimSpace(out.Stderr))
		}
	}
	return out, nil
}

type containerState struct {
	Status    string
	Running   bool
	OOMKilled bool
	ExitCode  int
	Error     string
}

func inspectState(ctx context.Context, name string) (containerState, error) {
	var st containerState
	b, err := docker(ctx, "inspect", "--type", "container", "--format", "{{json .State}}", name)
	if err != nil {
		return st, fmt.Errorf("inspect container: %w", err)
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &st); err != nil {
		return st, fmt.Errorf("parse container state: %w", err)
	}
	return st, nil
}

// createArgs builds the docker create command line. Values are passed in
// --flag=value form so no value can be read as a flag.
func createArgs(s Spec, name, runtime string) ([]string, error) {
	if s.Image == "" || strings.HasPrefix(s.Image, "-") {
		return nil, fmt.Errorf("sandbox: bad image %q", s.Image)
	}
	if len(s.Cmd) == 0 {
		return nil, errors.New("sandbox: empty command")
	}
	tmpfs := s.TmpfsMB
	if tmpfs <= 0 {
		tmpfs = DefaultTmpfsMB
	}
	pids := s.PidsLimit
	if pids <= 0 {
		pids = DefaultPidsLimit
	}
	mem := s.MemoryMB
	if mem <= 0 {
		mem = DefaultMemoryMB
	}
	cpus := s.CPUs
	if cpus <= 0 {
		cpus = DefaultCPUs
	}

	args := []string{"create",
		"--name=" + name,
		"--label=reprobot=1",
		"--log-driver=none",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--pids-limit=" + strconv.Itoa(pids),
		fmt.Sprintf("--memory=%dm", mem),
		fmt.Sprintf("--memory-swap=%dm", mem),
		"--cpus=" + strconv.FormatFloat(cpus, 'f', -1, 64),
		"--ulimit=core=0",
		fmt.Sprintf("--tmpfs=/tmp:rw,nosuid,nodev,size=%dm", tmpfs),
	}
	if !s.Network {
		args = append(args, "--network=none")
	}
	if s.ReadOnlyRoot {
		args = append(args, "--read-only")
	}
	if s.User != "" {
		args = append(args, "--user="+s.User)
	}
	if runtime == "runsc" {
		args = append(args, "--runtime=runsc")
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, fmt.Errorf("sandbox: bad env name %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env="+k+"="+s.Env[k])
	}
	if s.WorkDir != "" {
		if !filepath.IsAbs(s.WorkDir) {
			return nil, fmt.Errorf("sandbox: workdir %q is not absolute", s.WorkDir)
		}
		args = append(args, "--workdir="+s.WorkDir)
	}
	for _, m := range s.Mounts {
		// A relative -v source names a docker volume, a missing one is
		// created by the daemon, and ":" splits the -v value.
		if !filepath.IsAbs(m.Source) || !filepath.IsAbs(m.Target) ||
			strings.Contains(m.Source, ":") || strings.Contains(m.Target, ":") {
			return nil, fmt.Errorf("sandbox: bad mount %q -> %q", m.Source, m.Target)
		}
		if _, err := os.Stat(m.Source); err != nil {
			return nil, fmt.Errorf("sandbox: mount source: %w", err)
		}
		v := m.Source + ":" + m.Target
		if m.ReadOnly {
			v += ":ro"
		}
		args = append(args, "--volume="+v)
	}
	args = append(args, s.Image)
	args = append(args, s.Cmd...)
	return args, nil
}

// ImageDigest returns the content digest of an image, pulling it if needed.
// It returns the sha256 digest from RepoDigests, or the image ID when the
// image has no repo digest (for example, a locally built image).
func ImageDigest(ctx context.Context, image string) (string, error) {
	if image == "" || strings.HasPrefix(image, "-") {
		return "", fmt.Errorf("sandbox: bad image %q", image)
	}
	info, err := inspectImage(ctx, image)
	if err != nil {
		if _, perr := docker(ctx, "pull", "--quiet", image); perr != nil {
			return "", fmt.Errorf("image %s: %v; pull: %w", image, err, perr)
		}
		if info, err = inspectImage(ctx, image); err != nil {
			return "", err
		}
	}
	return pickDigest(image, info.RepoDigests, info.ID), nil
}

type imageInfo struct {
	ID          string `json:"Id"`
	RepoDigests []string
}

func inspectImage(ctx context.Context, image string) (imageInfo, error) {
	var info imageInfo
	b, err := docker(ctx, "image", "inspect", "--format", "{{json .}}", image)
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &info); err != nil {
		return info, fmt.Errorf("parse image info: %w", err)
	}
	return info, nil
}

// pickDigest prefers the repo digest of the image's own repository; an
// image tagged under several names has one repo digest per name.
func pickDigest(image string, repoDigests []string, id string) string {
	want := normalizeRepo(image)
	first := ""
	for _, rd := range repoDigests {
		repo, dg, ok := strings.Cut(rd, "@")
		if !ok {
			continue
		}
		if first == "" {
			first = dg
		}
		if normalizeRepo(repo) == want {
			return dg
		}
	}
	if first != "" {
		return first
	}
	return id
}

// normalizeRepo strips the tag or digest and the default Docker Hub prefix.
func normalizeRepo(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	ref = strings.TrimPrefix(ref, "docker.io/")
	ref = strings.TrimPrefix(ref, "library/")
	return ref
}

func containerName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "reprobot-" + hex.EncodeToString(b), nil
}

// docker runs one docker CLI command and returns its stdout.
func docker(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2048 {
			msg = msg[len(msg)-2048:]
		}
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, msg)
	}
	return stdout.Bytes(), nil
}

// headBuffer keeps the first limit bytes written to it.
type headBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (h *headBuffer) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if room := h.limit - len(h.buf); room > 0 {
		h.buf = append(h.buf, p[:min(room, len(p))]...)
	}
	// Report a full write so the copy keeps draining the pipe.
	return len(p), nil
}

func (h *headBuffer) String() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.buf)
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	// Trim only when the buffer is twice the limit, so the copy cost
	// stays linear in the bytes written.
	if len(t.buf) >= 2*t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > t.limit {
		return string(t.buf[len(t.buf)-t.limit:])
	}
	return string(t.buf)
}
