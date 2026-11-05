// Package build builds a project commit with a maintainer profile, with a
// content-addressed cache.
//
// Cache layout under CacheDir:
//
//	builds/<key>/bin   profile outputs
//	builds/<key>/done  marker, written before the entry is renamed into place
//	builds/<key>.lock  flock that serializes builds of one key
//
// An entry is built in a temp dir and renamed into place in one step, so a
// crash mid-build never leaves a half entry. Failed builds are not cached.
package build

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/sandbox"
	"github.com/DCchoudhury15/reprobot/internal/source"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// ErrBuildFailed means the profile did not build at that commit. Output
// still carries the log tail.
var ErrBuildFailed = errors.New("build failed")

// Builder builds in a sandbox and caches outputs under CacheDir.
type Builder struct {
	CacheDir string

	// run starts the build sandbox; nil means sandbox.Run. Tests use it to
	// count containers.
	run func(context.Context, sandbox.Spec) (sandbox.Outcome, error)
}

// Output describes a finished (or cached) build.
type Output struct {
	BinDir      string // directory holding the profile outputs
	Key         string
	CacheHit    bool
	LogTail     string
	ImageDigest string
	Runtime     string // runsc | runc
}

// Build defaults, used when the project limits are zero.
const (
	DefaultCPUs           = 2
	DefaultMemoryMB       = 2048
	DefaultTimeoutSeconds = 900
	buildTmpfsMB          = 512
	logTailBytes          = 4096
)

// Key returns the build cache key: sha256 hex of project id, commit,
// profile name, profile content and image digest.
//
// Every field is length-prefixed, outputs are sorted and env is sorted by
// name, so the key does not depend on map order and no two different
// inputs share an encoding.
func Key(projectID, sha, profileName string, profile spec.Profile, imageDigest string) string {
	h := sha256.New()
	putString(h, "reprobot-build-v1")
	putString(h, projectID)
	putString(h, sha)
	putString(h, profileName)
	putString(h, profile.Build)
	outputs := slices.Sorted(slices.Values(profile.Outputs))
	putUint(h, uint64(len(outputs)))
	for _, o := range outputs {
		putString(h, o)
	}
	names := make([]string, 0, len(profile.Env))
	for k := range profile.Env {
		names = append(names, k)
	}
	slices.Sort(names)
	putUint(h, uint64(len(names)))
	for _, k := range names {
		putString(h, k)
		putString(h, profile.Env[k])
	}
	putString(h, imageDigest)
	return hex.EncodeToString(h.Sum(nil))
}

func putUint(h hash.Hash, n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	h.Write(b[:])
}

func putString(h hash.Hash, s string) {
	putUint(h, uint64(len(s)))
	io.WriteString(h, s)
}

// Build builds sha with the named profile, or returns cached outputs.
func (b *Builder) Build(ctx context.Context, p *spec.Project, profileName, sha, mirrorDir string) (Output, error) {
	if p == nil {
		return Output{}, errors.New("build: nil project")
	}
	prof, ok := p.Profiles[profileName]
	if !ok {
		return Output{}, fmt.Errorf("build: project %s has no profile %q", p.ID, profileName)
	}
	if strings.TrimSpace(prof.Build) == "" {
		return Output{}, fmt.Errorf("build: profile %q has an empty build command", profileName)
	}
	if err := checkOutputs(prof.Outputs); err != nil {
		return Output{}, fmt.Errorf("build: profile %q: %w", profileName, err)
	}
	if b.CacheDir == "" {
		return Output{}, errors.New("build: empty cache dir")
	}
	cacheDir, err := filepath.Abs(b.CacheDir)
	if err != nil {
		return Output{}, err
	}

	digest, err := sandbox.ImageDigest(ctx, p.Image)
	if err != nil {
		return Output{}, err
	}
	rt, err := sandbox.Runtime(ctx)
	if err != nil {
		return Output{}, err
	}
	key := Key(p.ID, sha, profileName, prof, digest)
	buildsDir := filepath.Join(cacheDir, "builds")
	entry := filepath.Join(buildsDir, key)
	// BinDir is set only when the outputs exist.
	out := Output{Key: key, ImageDigest: digest, Runtime: rt}
	hit := Output{BinDir: filepath.Join(entry, "bin"), Key: key, CacheHit: true, ImageDigest: digest, Runtime: rt}
	if isDone(entry) {
		return hit, nil
	}

	if err := os.MkdirAll(buildsDir, 0o755); err != nil {
		return Output{}, err
	}
	unlock, err := lockFile(ctx, filepath.Join(buildsDir, key+".lock"))
	if err != nil {
		return Output{}, err
	}
	defer unlock()
	// Another builder may have finished this key while we waited.
	if isDone(entry) {
		return hit, nil
	}

	work, err := os.MkdirTemp(buildsDir, ".tmp-"+key[:16]+"-")
	if err != nil {
		return Output{}, err
	}
	defer removeAll(work)
	srcDir := filepath.Join(work, "src")
	if err := source.Export(ctx, mirrorDir, sha, srcDir); err != nil {
		return Output{}, err
	}

	res, err := b.runner()(ctx, buildSpec(p, prof, srcDir))
	if err != nil {
		return Output{}, fmt.Errorf("build sandbox: %w", err)
	}
	out.LogTail = logTail(res.Stdout, res.Stderr)
	switch {
	case res.TimedOut:
		return out, fmt.Errorf("%w: timed out", ErrBuildFailed)
	case res.OOMKilled:
		return out, fmt.Errorf("%w: out of memory", ErrBuildFailed)
	case res.StartError != "":
		return out, fmt.Errorf("%w: %s", ErrBuildFailed, res.StartError)
	case res.ExitCode != 0:
		return out, fmt.Errorf("%w: exit code %d", ErrBuildFailed, res.ExitCode)
	}

	stage := filepath.Join(work, "entry")
	binDir := filepath.Join(stage, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return Output{}, err
	}
	// Readable by the unprivileged run user, whatever the umask is.
	if err := os.Chmod(stage, 0o755); err != nil {
		return Output{}, err
	}
	if err := os.Chmod(binDir, 0o755); err != nil {
		return Output{}, err
	}
	if err := copyOutputs(srcDir, binDir, prof.Outputs); err != nil {
		return out, fmt.Errorf("%w: %v", ErrBuildFailed, err)
	}
	if err := os.WriteFile(filepath.Join(stage, "done"), []byte(key+"\n"), 0o644); err != nil {
		return Output{}, err
	}
	// A dir without a marker cannot come from a rename; clear it so the
	// rename can take its place.
	if _, err := os.Lstat(entry); err == nil {
		removeAll(entry)
	}
	if err := os.Rename(stage, entry); err != nil {
		return Output{}, err
	}
	out.BinDir = hit.BinDir
	return out, nil
}

func (b *Builder) runner() func(context.Context, sandbox.Spec) (sandbox.Outcome, error) {
	if b.run != nil {
		return b.run
	}
	return sandbox.Run
}

// buildSpec is the sandbox for one build: no network, read-only root, the
// source tree at /src as the only writable mount, run as the current user
// so every file it creates stays removable.
func buildSpec(p *spec.Project, prof spec.Profile, srcDir string) sandbox.Spec {
	cpus := p.Limits.CPUs
	if cpus <= 0 {
		cpus = DefaultCPUs
	}
	mem := p.Limits.MemoryMB
	if mem <= 0 {
		mem = DefaultMemoryMB
	}
	timeout := p.Limits.BuildTimeoutSeconds
	if timeout <= 0 {
		timeout = DefaultTimeoutSeconds
	}
	env := map[string]string{"HOME": "/tmp"}
	for k, v := range prof.Env {
		env[k] = v
	}
	return sandbox.Spec{
		Image:        p.Image,
		Cmd:          []string{"sh", "-c", prof.Build},
		User:         fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		Env:          env,
		WorkDir:      "/src",
		Mounts:       []sandbox.Mount{{Source: srcDir, Target: "/src"}},
		Network:      false,
		CPUs:         cpus,
		MemoryMB:     mem,
		Timeout:      time.Duration(timeout) * time.Second,
		ReadOnlyRoot: true,
		TmpfsMB:      buildTmpfsMB,
	}
}

// checkOutputs validates the profile outputs: relative paths inside the
// source tree, with distinct file names because they all land in bin/.
func checkOutputs(outputs []string) error {
	if len(outputs) == 0 {
		return errors.New("no outputs")
	}
	seen := map[string]bool{}
	for _, o := range outputs {
		rel := filepath.Clean(filepath.FromSlash(o))
		if o == "" || filepath.IsAbs(rel) || rel == "." || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("bad output path %q", o)
		}
		name := filepath.Base(rel)
		if seen[name] {
			return fmt.Errorf("two outputs named %q", name)
		}
		seen[name] = true
	}
	return nil
}

// copyOutputs copies each output from srcDir into binDir under its base
// name. An output may be a symlink, but only to a regular file inside
// srcDir.
func copyOutputs(srcDir, binDir string, outputs []string) error {
	realSrc, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		return err
	}
	for _, o := range outputs {
		rel := filepath.Clean(filepath.FromSlash(o))
		real, err := filepath.EvalSymlinks(filepath.Join(srcDir, rel))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("output %s was not built", o)
			}
			return fmt.Errorf("output %s: %w", o, err)
		}
		if r, err := filepath.Rel(realSrc, real); err != nil || r == ".." ||
			strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return fmt.Errorf("output %s points outside the source tree", o)
		}
		fi, err := os.Stat(real)
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("output %s is not a regular file", o)
		}
		mode := os.FileMode(0o644)
		if fi.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		if err := copyFile(real, filepath.Join(binDir, filepath.Base(rel)), mode); err != nil {
			return fmt.Errorf("output %s: %w", o, err)
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// logTail returns the last logTailBytes of stdout followed by stderr.
func logTail(stdout, stderr string) string {
	s := stdout
	if s != "" && stderr != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += stderr
	if len(s) > logTailBytes {
		s = s[len(s)-logTailBytes:]
	}
	return s
}

func isDone(entry string) bool {
	_, err := os.Stat(filepath.Join(entry, "done"))
	return err == nil
}

// removeAll removes dir, first making directories writable if a build left
// read-only ones behind.
func removeAll(dir string) {
	if os.RemoveAll(dir) == nil {
		return
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}

// lockFile takes an exclusive flock on path, waiting until ctx is done.
// flock locks belong to the open file, so goroutines in one process also
// exclude each other.
func lockFile(ctx context.Context, path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
