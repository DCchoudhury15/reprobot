// Package source fetches project git repos and exports exact commits.
package source

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// ErrUnknownRef means the repo was fetched but the ref (sha or tag) does
// not exist in it. Callers treat it as bad input, not an infrastructure error.
var ErrUnknownRef = errors.New("unknown ref")

// Resolve fetches repo (URL or local path) into a mirror under cacheDir and
// returns the mirror path and the full commit sha for ref (sha or tag).
//
// The mirror lives at cacheDir/git/<first 16 hex of sha256(repo)>.git. A
// per-mirror file lock keeps concurrent callers from colliding.
func Resolve(ctx context.Context, cacheDir, repo, ref string) (mirrorDir, sha string, err error) {
	// Check the ref before any git command runs, so it can never be read
	// as an option.
	if err := checkRef(ref); err != nil {
		return "", "", err
	}
	if repo == "" || strings.HasPrefix(repo, "-") {
		return "", "", fmt.Errorf("source: bad repo %q", repo)
	}
	gitDir := filepath.Join(cacheDir, "git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(repo))
	key := hex.EncodeToString(sum[:])[:16]
	mirrorDir = filepath.Join(gitDir, key+".git")

	unlock, err := lockFile(ctx, filepath.Join(gitDir, key+".lock"))
	if err != nil {
		return "", "", err
	}
	defer unlock()

	if err := update(ctx, gitDir, mirrorDir, repo); err != nil {
		return "", "", err
	}
	out, err := git(ctx, "-C", mirrorDir, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", fmt.Errorf("%w: %q in %s", ErrUnknownRef, ref, repo)
	}
	return mirrorDir, strings.TrimSpace(string(out)), nil
}

// update clones the mirror when it is missing and fetches it otherwise.
// The caller holds the mirror lock.
func update(ctx context.Context, gitDir, mirrorDir, repo string) error {
	if _, err := os.Stat(mirrorDir); err == nil {
		if _, err := git(ctx, "-C", mirrorDir, "fetch", "--quiet", "--prune", "origin"); err != nil {
			return fmt.Errorf("fetch %s: %w", repo, err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Clone into a temp dir and rename, so a crash never leaves a half
	// mirror at the final path.
	tmp, err := os.MkdirTemp(gitDir, ".clone-")
	if err != nil {
		return err
	}
	if _, err := git(ctx, "clone", "--mirror", "--quiet", "--", repo, tmp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	if err := os.Rename(tmp, mirrorDir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// checkRef rejects refs that git could read as an option or that cannot
// be a single revision.
func checkRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") ||
		strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%w: invalid ref %q", ErrUnknownRef, ref)
	}
	return nil
}

// isFullSHA reports whether s is a full sha1 or sha256 object id.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Export writes the tree at sha into dest, without the .git directory.
// dest is created if needed and should be empty.
func Export(ctx context.Context, mirrorDir, sha, dest string) error {
	if !isFullSHA(sha) {
		return fmt.Errorf("source: export needs a full commit sha, got %q", sha)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", mirrorDir, "archive", "--format=tar", sha)
	cmd.Env = gitEnv()
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	r, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	xerr := extractTar(r, dest)
	if xerr != nil {
		cancel()
	}
	werr := cmd.Wait()
	if xerr != nil {
		return fmt.Errorf("export %s: %w", sha, xerr)
	}
	if werr != nil {
		return fmt.Errorf("git archive %s: %w: %s", sha, werr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// extractTar writes a tar stream into dest. It rejects absolute names,
// names with "..", entries under a symlink, and symlinks whose target
// leaves dest. It never follows a symlink while writing.
func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // git stores the commit id here
		}
		rel, err := cleanName(h.Name)
		if err != nil {
			return err
		}
		if rel == "." {
			continue
		}
		if err := noSymlinkParents(dest, rel); err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		perm := os.FileMode(h.Mode) & 0o777
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, perm|0o700); err != nil && !isDir(target) {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// O_EXCL fails on any existing path, symlinks included.
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			link := h.Linkname
			if link == "" || filepath.IsAbs(link) {
				return fmt.Errorf("symlink %s -> %q leaves the tree", h.Name, link)
			}
			resolved := filepath.Join(filepath.Dir(rel), filepath.FromSlash(link))
			if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
				return fmt.Errorf("symlink %s -> %q leaves the tree", h.Name, link)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry %s (type %q)", h.Name, h.Typeflag)
		}
	}
}

// cleanName turns a tar entry name into a clean relative path.
func cleanName(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return "", fmt.Errorf("bad entry name %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("bad entry name %q", name)
		}
	}
	return filepath.Clean(filepath.FromSlash(name)), nil
}

// noSymlinkParents fails when any parent directory of rel inside dest is a
// symlink. Git trees never put entries under a symlink, and refusing them
// means lexical symlink checks match what the file system does.
func noSymlinkParents(dest, rel string) error {
	p := dest
	parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
	for _, part := range parts {
		if part == "." {
			continue
		}
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("entry %s is under a symlink", rel)
		}
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func gitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

// git runs one git command and returns its stdout.
func git(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
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
