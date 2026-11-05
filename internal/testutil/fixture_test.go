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
