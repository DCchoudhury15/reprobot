package source

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.org",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolve(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	ctx := context.Background()
	cases := map[string]string{
		f.Vuln:     f.Vuln,
		f.Fix[:7]:  f.Fix,
		"HEAD":     f.Revert,
		"main":     f.Revert,
		"HEAD~2":   f.Vuln,
		f.Revert:   f.Revert,
		f.Vuln[:9]: f.Vuln,
	}
	var mirror string
	for ref, want := range cases {
		m, sha, err := Resolve(ctx, cache, f.RepoDir, ref)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		if sha != want {
			t.Errorf("Resolve(%q) = %s, want %s", ref, sha, want)
		}
		if mirror == "" {
			mirror = m
		} else if m != mirror {
			t.Errorf("mirror changed: %s vs %s", m, mirror)
		}
	}
	if !strings.HasPrefix(mirror, filepath.Join(cache, "git")+string(filepath.Separator)) || !strings.HasSuffix(mirror, ".git") {
		t.Errorf("mirror path %s", mirror)
	}
	if len(filepath.Base(mirror)) != 16+len(".git") {
		t.Errorf("mirror name %s, want 16 hex + .git", filepath.Base(mirror))
	}
}

func TestResolveUnknownRef(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	for _, ref := range []string{"0123456789abcdef0123456789abcdef01234567", "nosuchtag", "v9.9"} {
		_, _, err := Resolve(context.Background(), cache, f.RepoDir, ref)
		if !errors.Is(err, ErrUnknownRef) {
			t.Errorf("Resolve(%q) err = %v, want ErrUnknownRef", ref, err)
		}
	}
	// A tree or blob is not a commit.
	tree := gitIn(t, f.RepoDir, "rev-parse", f.Vuln+"^{tree}")
	if _, _, err := Resolve(context.Background(), cache, f.RepoDir, tree); !errors.Is(err, ErrUnknownRef) {
		t.Errorf("tree id: err = %v, want ErrUnknownRef", err)
	}
}

func TestResolveFetchFailureIsNotUnknownRef(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	_, _, err := Resolve(context.Background(), t.TempDir(), missing, "HEAD")
	if err == nil || errors.Is(err, ErrUnknownRef) {
		t.Fatalf("err = %v, want a non ErrUnknownRef error", err)
	}

	// Fetch failure on an existing mirror: the source repo is gone.
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	if _, _, err := Resolve(context.Background(), cache, f.RepoDir, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.RepoDir); err != nil {
		t.Fatal(err)
	}
	_, _, err = Resolve(context.Background(), cache, f.RepoDir, f.Vuln)
	if err == nil || errors.Is(err, ErrUnknownRef) {
		t.Fatalf("fetch failure: err = %v, want a non ErrUnknownRef error", err)
	}
}

func TestResolveRejectsBadRefs(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	for _, ref := range []string{"--upload-pack=x", "-n", "", "main HEAD", "a\tb", "main\n", "x\x00y"} {
		cache := t.TempDir()
		_, _, err := Resolve(context.Background(), cache, f.RepoDir, ref)
		if err == nil {
			t.Errorf("Resolve(%q) = nil error", ref)
			continue
		}
		// Rejected before any git command: nothing was cloned.
		if _, serr := os.Stat(filepath.Join(cache, "git")); serr == nil {
			entries, _ := os.ReadDir(filepath.Join(cache, "git"))
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".git") {
					t.Errorf("Resolve(%q) cloned %s before rejecting", ref, e.Name())
				}
			}
		}
	}
}

func TestExport(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	ctx := context.Background()
	for _, c := range []struct{ sha, want string }{
		{f.Vuln, "vuln"},
		{f.Fix, "fixed"},
		{f.Revert, "vuln"},
	} {
		mirror, sha, err := Resolve(ctx, cache, f.RepoDir, c.sha)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "src")
		if err := Export(ctx, mirror, sha, dest); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, "tinyparse.c"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(testutil.FixtureDir(), c.want, "tinyparse.c"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("tinyparse.c at %s does not match %s", sha[:7], c.want)
		}
		if _, err := os.Stat(filepath.Join(dest, "Makefile")); err != nil {
			t.Errorf("Makefile missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			t.Error(".git exported")
		}
	}
}

func TestExportRejectsShortSHA(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	mirror, _, err := Resolve(context.Background(), t.TempDir(), f.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, sha := range []string{f.Vuln[:7], "--output=/tmp/x", "HEAD"} {
		if err := Export(context.Background(), mirror, sha, t.TempDir()); err == nil {
			t.Errorf("Export(%q) = nil error", sha)
		}
	}
}

func TestExportKeepsModesAndInnerSymlinks(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "data.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("scripts/run.sh", filepath.Join(repo, "run")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "init")

	mirror, sha, err := Resolve(context.Background(), t.TempDir(), repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := Export(context.Background(), mirror, sha, dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh mode %v lost the executable bit", fi.Mode())
	}
	fi, err = os.Stat(filepath.Join(dest, "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Errorf("data.txt mode %v is executable", fi.Mode())
	}
	if l, err := os.Readlink(filepath.Join(dest, "run")); err != nil || l != "scripts/run.sh" {
		t.Errorf("symlink run = %q, %v", l, err)
	}
}

func TestResolveSeesNewCommit(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	ctx := context.Background()
	if _, sha, err := Resolve(ctx, cache, f.RepoDir, "HEAD"); err != nil || sha != f.Revert {
		t.Fatalf("first Resolve = %s, %v", sha, err)
	}
	if err := os.WriteFile(filepath.Join(f.RepoDir, "NEWS"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.RepoDir, "add", "NEWS")
	gitIn(t, f.RepoDir, "commit", "-q", "-m", "news")
	newSHA := gitIn(t, f.RepoDir, "rev-parse", "HEAD")
	gitIn(t, f.RepoDir, "tag", "v1.0")

	for _, ref := range []string{"HEAD", newSHA, "v1.0"} {
		_, sha, err := Resolve(ctx, cache, f.RepoDir, ref)
		if err != nil {
			t.Fatalf("Resolve(%q) after commit: %v", ref, err)
		}
		if sha != newSHA {
			t.Errorf("Resolve(%q) = %s, want new commit %s", ref, sha, newSHA)
		}
	}
}

func TestResolveConcurrent(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	cache := t.TempDir()
	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	shas := make([]string, n)
	mirrors := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mirrors[i], shas[i], errs[i] = Resolve(context.Background(), cache, f.RepoDir, f.Vuln)
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Errorf("call %d: %v", i, errs[i])
			continue
		}
		if shas[i] != f.Vuln || mirrors[i] != mirrors[0] {
			t.Errorf("call %d: %s %s", i, mirrors[i], shas[i])
		}
	}
	// Only the final mirror is left; temp clones are gone.
	entries, err := os.ReadDir(filepath.Join(cache, "git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".clone-") {
			t.Errorf("leftover temp clone %s", e.Name())
		}
	}
}

type entry struct {
	name, link string
	typ        byte
	body       string
}

func makeTar(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Linkname: e.link, Mode: 0o644, Size: int64(len(e.body))}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractTarRejects(t *testing.T) {
	cases := map[string][]entry{
		"absolute name":     {{name: "/etc/evil", typ: tar.TypeReg, body: "x"}},
		"dotdot name":       {{name: "a/../../evil", typ: tar.TypeReg, body: "x"}},
		"absolute symlink":  {{name: "l", typ: tar.TypeSymlink, link: "/etc/passwd"}},
		"escaping symlink":  {{name: "a/l", typ: tar.TypeSymlink, link: "../../x"}},
		"dotdot symlink":    {{name: "l", typ: tar.TypeSymlink, link: ".."}},
		"write via symlink": {{name: "l", typ: tar.TypeSymlink, link: "."}, {name: "l/x", typ: tar.TypeReg, body: "x"}},
		// Lexically "d/l/x -> .." stays inside, but d/l points at the top,
		// so the link would really land at the top and point outside.
		"chained symlink": {
			{name: "d/", typ: tar.TypeDir},
			{name: "d/l", typ: tar.TypeSymlink, link: ".."},
			{name: "d/l/x", typ: tar.TypeSymlink, link: ".."},
		},
		"overwrite symlink": {{name: "f", typ: tar.TypeSymlink, link: "g"}, {name: "f", typ: tar.TypeReg, body: "x"}},
		"hard link":         {{name: "f", typ: tar.TypeReg, body: "x"}, {name: "h", typ: tar.TypeLink, link: "f"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "dest")
			if err := os.Mkdir(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extractTar(makeTar(t, entries...), dest); err == nil {
				t.Fatal("extractTar = nil error")
			}
			// Nothing may appear outside dest.
			out, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 {
				t.Fatalf("files written outside dest: %v", out)
			}
		})
	}
}

func TestExtractTarAllowsInnerSymlinks(t *testing.T) {
	dest := t.TempDir()
	err := extractTar(makeTar(t,
		entry{name: "a/", typ: tar.TypeDir},
		entry{name: "a/f", typ: tar.TypeReg, body: "hi"},
		entry{name: "a/up", typ: tar.TypeSymlink, link: "../b"},
		entry{name: "self", typ: tar.TypeSymlink, link: "."},
	), dest)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "a", "f")); err != nil || string(b) != "hi" {
		t.Fatalf("a/f = %q, %v", b, err)
	}
}
