package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

// sparse makes a file of the given size without using disk space.
func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestFilePathsOK(t *testing.T) {
	base := t.TempDir()
	writeFile(t, base, "input.bin", "data")
	writeFile(t, base, "sub/real.bin", "more")
	symlink(t, "sub/real.bin", filepath.Join(base, "alias.bin"))
	r := &Reproducer{Files: []string{"input.bin", "alias.bin", "sub/real.bin"}}

	// Reach base through a symlinked dir: the result must still be accepted
	// and point at the real files.
	linkDir := filepath.Join(t.TempDir(), "link")
	symlink(t, base, linkDir)
	got, err := r.FilePaths(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(base, "input.bin"),
		filepath.Join(base, "sub/real.bin"),
		filepath.Join(base, "sub/real.bin"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFilePathsRejects(t *testing.T) {
	outside := t.TempDir()
	secret := writeFile(t, outside, "secret", "s")
	cases := []struct {
		name  string
		files []string
		setup func(t *testing.T, base string)
		want  string
	}{
		{"missing", []string{"nope.bin"}, nil, "no such file"},
		{"absolute", []string{secret}, nil, "is absolute"},
		{"parent", []string{"../secret"}, nil, `contains ".."`},
		{"not clean", []string{"a/../../secret"}, nil, "not a clean path"},
		{"symlink to outside file", []string{"link"}, func(t *testing.T, base string) {
			symlink(t, secret, filepath.Join(base, "link"))
		}, "outside the repro dir"},
		{"relative symlink to outside", []string{"link"}, func(t *testing.T, base string) {
			rel, err := filepath.Rel(base, secret)
			if err != nil {
				t.Fatal(err)
			}
			symlink(t, rel, filepath.Join(base, "link"))
		}, "outside the repro dir"},
		{"symlinked dir to outside", []string{"dir/secret"}, func(t *testing.T, base string) {
			symlink(t, outside, filepath.Join(base, "dir"))
		}, "outside the repro dir"},
		{"symlink to parent dir", []string{"up"}, func(t *testing.T, base string) {
			symlink(t, "..", filepath.Join(base, "up"))
		}, "outside the repro dir"},
		{"directory", []string{"sub"}, func(t *testing.T, base string) {
			if err := os.Mkdir(filepath.Join(base, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"fifo", []string{"pipe"}, func(t *testing.T, base string) {
			if err := syscall.Mkfifo(filepath.Join(base, "pipe"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "not a regular file"},
		{"file too big", []string{"big"}, func(t *testing.T, base string) {
			sparse(t, filepath.Join(base, "big"), MaxFileBytes+1)
		}, "larger than 10485760 bytes"},
		{"total too big", []string{"a", "b", "c", "d", "e", "f"}, func(t *testing.T, base string) {
			for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
				sparse(t, filepath.Join(base, n), 9<<20)
			}
		}, "in total"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, base)
			}
			r := &Reproducer{Files: tc.files}
			got, err := r.FilePaths(base)
			if err == nil {
				t.Fatalf("want error containing %q, got %q", tc.want, got)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error does not wrap ErrInvalid: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestFilePathsSizeLimitsAreInclusive(t *testing.T) {
	base := t.TempDir()
	names := []string{"a", "b", "c", "d", "e"}
	for _, n := range names {
		sparse(t, filepath.Join(base, n), MaxFileBytes)
	}
	r := &Reproducer{Files: names}
	if _, err := r.FilePaths(base); err != nil {
		t.Fatalf("5 files of exactly 10 MB must pass: %v", err)
	}
}

// reproDir writes repro.yaml and files into a new dir and returns the
// repro.yaml path and the loaded reproducer.
func reproDir(t *testing.T, yamlText string, files map[string]string) (string, *Reproducer) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	p := writeFile(t, dir, "repro.yaml", yamlText)
	r, err := LoadReproducer(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, r
}

func mustHash(t *testing.T, p string, r *Reproducer) string {
	t.Helper()
	h, err := ReproducerHash(p, r)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestReproducerHashGolden(t *testing.T) {
	// Job keys and stored results use this hash, so the encoding must not
	// change by accident. If it changes on purpose, bump the version tag.
	// The value was computed outside Go from the documented encoding.
	y := reproYAML(map[string]string{"files": "files: [b.bin, a.bin]"})
	p, r := reproDir(t, y, map[string]string{"a.bin": "AAAA", "b.bin": "B"})
	const want = "43a720b1f31d0cbff264877cfe3438c7148a912832cabbcbb733c9ffb89ad8bd"
	if got := mustHash(t, p, r); got != want {
		t.Errorf("hash changed: got %s, want %s", got, want)
	}
}

func TestReproducerHashChanges(t *testing.T) {
	y := reproYAML(map[string]string{"files": "files: [a.bin, b.bin]"})
	p1, r1 := reproDir(t, y, map[string]string{"a.bin": "AAAA", "b.bin": "B"})
	h1 := mustHash(t, p1, r1)
	if len(h1) != 64 || strings.Trim(h1, "0123456789abcdef") != "" {
		t.Fatalf("not a sha256 hex: %q", h1)
	}

	p2, r2 := reproDir(t, y, map[string]string{"a.bin": "AAAA", "b.bin": "B"})
	if h2 := mustHash(t, p2, r2); h2 != h1 {
		t.Errorf("same input, different hash: %s %s", h1, h2)
	}

	p3, r3 := reproDir(t, y, map[string]string{"a.bin": "AAAA", "b.bin": "C"})
	if h3 := mustHash(t, p3, r3); h3 == h1 {
		t.Error("changed file content, same hash")
	}

	p4, r4 := reproDir(t, y+"# comment\n", map[string]string{"a.bin": "AAAA", "b.bin": "B"})
	if h4 := mustHash(t, p4, r4); h4 == h1 {
		t.Error("changed repro.yaml bytes, same hash")
	}
}

func TestReproducerHashNoSplitCollision(t *testing.T) {
	// Plain concatenation of yaml + name + content gives the same bytes
	// for these two inputs: the second yaml has one more byte (inside a
	// comment) and its file has one byte less.
	y := reproYAML(map[string]string{"files": "files: [x]"}) + "#"
	p1, r1 := reproDir(t, y, map[string]string{"x": "xx"})
	p2, r2 := reproDir(t, y+"x", map[string]string{"x": "x"})
	plain := func(p string, content string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "x" + content
	}
	if plain(p1, "xx") != plain(p2, "x") {
		t.Fatal("test setup: plain concatenations should be equal")
	}
	if mustHash(t, p1, r1) == mustHash(t, p2, r2) {
		t.Error("different inputs with equal plain concatenation give the same hash")
	}
}

func TestReproducerHashChecksFiles(t *testing.T) {
	dir := t.TempDir()
	outside := writeFile(t, t.TempDir(), "secret", "s")
	symlink(t, outside, filepath.Join(dir, "input.bin"))
	p := writeFile(t, dir, "repro.yaml", reproYAML(nil))
	r, err := LoadReproducer(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReproducerHash(p, r); !errors.Is(err, ErrInvalid) {
		t.Errorf("want ErrInvalid for a file outside the repro dir, got %v", err)
	}
}

func TestFixtureInputsLoad(t *testing.T) {
	fx := testutil.NewTinyparseRepo(t)
	projectPath, reproPath := testutil.WriteInputs(t, fx.RepoDir, fx.Vuln)

	proj, err := LoadProject(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if proj.ID != "tinyparse" || proj.Repo != fx.RepoDir || proj.Image != "gcc:15" {
		t.Errorf("wrong project: %+v", proj)
	}
	if _, ok := proj.Profiles["ubsan"]; !ok {
		t.Errorf("ubsan profile missing")
	}
	if want := (Limits{CPUs: 1, MemoryMB: 1024, BuildTimeoutSeconds: 300}); proj.Limits != want {
		t.Errorf("limits: got %+v, want %+v", proj.Limits, want)
	}

	r, err := LoadReproducer(reproPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.Commit != fx.Vuln || r.Run.TimeoutSeconds != 20 || r.Run.Repeat != 3 {
		t.Errorf("wrong reproducer: %+v", r)
	}
	if !strings.Contains(r.Claim.Text, "16 byte heap buffer") {
		t.Errorf("claim text not loaded: %q", r.Claim.Text)
	}
	if err := r.CheckAgainst(proj); err != nil {
		t.Error(err)
	}
	paths, err := r.FilePaths(filepath.Dir(reproPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "input.bin" {
		t.Errorf("paths: %q", paths)
	}
	if _, err := ReproducerHash(reproPath, r); err != nil {
		t.Error(err)
	}
}
