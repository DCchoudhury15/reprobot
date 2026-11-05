//go:build unix

package runner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The poc files must stay readable by the run uid even when the
// reprobot process has a strict umask.
func TestPocDirStrictUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	src := filepath.Join(t.TempDir(), "input.bin")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := makePocDir(t.TempDir(), []string{"a/b/input.bin"}, []string{src})
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{
		dir:                                 0o755,
		filepath.Join(dir, "a"):             0o755,
		filepath.Join(dir, "a", "b"):        0o755,
		filepath.Join(dir, "a/b/input.bin"): 0o644,
	} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s has mode %v, want %v", p, st.Mode().Perm(), want)
		}
	}
}
