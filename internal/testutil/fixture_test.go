package testutil

import "testing"

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
