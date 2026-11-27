package testutil

import (
	"os"
	"testing"
)

func TestNewTinyparseRepo(t *testing.T) {
	f := NewTinyparseRepo(t)
	if f.Vuln == f.Fix || f.Fix == f.Revert || f.Vuln == f.Revert {
		t.Fatalf("commits must differ: %+v", f)
	}
	p, r := WriteInputs(t, f.RepoDir, f.Vuln)
	if p == "" || r == "" {
		t.Fatal("empty paths")
	}
}

func TestNewTinyparseHistory(t *testing.T) {
	h := NewTinyparseHistory(t)
	if len(h.Commits) != 6 || h.BugAt != 4 || h.Broken != 2 {
		t.Fatalf("history %+v", h)
	}
	seen := map[string]bool{}
	for _, c := range h.Commits {
		if len(c) != 40 || seen[c] {
			t.Fatalf("bad or repeated commit %q", c)
		}
		seen[c] = true
	}
}

func TestNewTinyparseUBSanRepo(t *testing.T) {
	f := NewTinyparseUBSanRepo(t)
	if f.Vuln == f.Fix || f.Fix == f.Revert || f.Vuln == f.Revert {
		t.Fatalf("commits must differ: %+v", f)
	}
	b, err := os.ReadFile(UBSanInput())
	if err != nil || len(b) != 3 || b[0] < 0x80 {
		t.Fatalf("the input must be a count large enough to overflow: % x %v", b, err)
	}
}
