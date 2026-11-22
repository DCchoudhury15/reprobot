package result

import (
	"strings"
	"testing"
)

func TestCheckFix(t *testing.T) {
	crashA := &Crash{Kind: "heap-buffer-overflow", Signature: "A"}
	crashB := &Crash{Kind: "SEGV", Signature: "B"}
	at := func(state State, commit string, c *Crash) *Result {
		return &Result{State: state, Reason: "why", Inputs: Inputs{Commit: commit}, Crash: c}
	}
	before := at(ReproducedAsClaimed, "aaaaaaaaaa", crashA)
	flaky := at(Flaky, "bbbbbbbbbb", crashA)
	flaky.Crashed = 2
	flaky.Runs = make([]RunOutcome, 5)

	tests := []struct {
		name          string
		before, after *Result
		want          FixState
		reason        string
	}{
		{"fixed", before, at(NotReproduced, "bbbbbbbbbb", nil), Fixed, "crashed on aaaaaaa, no crash on bbbbbbb"},
		{"fixed after a different crash", at(ReproducedDifferent, "aaaaaaaaaa", crashA), at(NotReproduced, "b", nil), Fixed, "no crash on b"},
		{"same crash", before, at(ReproducedAsClaimed, "b", crashA), NotFixed, "with the same crash"},
		{"other crash", before, at(ReproducedDifferent, "b", crashB), NotFixed, "different crash (SEGV)"},
		{"some runs crash", before, flaky, NotFixed, "in 2 of 5 runs"},
		{"no crash data", before, at(ReproducedAsClaimed, "b", nil), NotFixed, "still crashes"},
		{"fix does not build", before, at(BuildFailed, "b", nil), CannotTell, "BUILD_FAILED: why"},
		{"fix times out", before, at(Timeout, "b", nil), CannotTell, "TIMEOUT"},
		{"fix commit unknown", before, at(InvalidInput, "b", nil), CannotTell, "INVALID_INPUT"},
		{"report does not reproduce", at(NotReproduced, "a", nil), at(NotReproduced, "b", nil), CannotTell, "does not reproduce on its own commit (NOT_REPRODUCED)"},
		{"report only flaky", flaky, at(NotReproduced, "b", nil), CannotTell, "(FLAKY)"},
		{"fix not run", at(NotReproduced, "a", nil), nil, CannotTell, "nothing to check"},
		{"fix missing", before, nil, CannotTell, "not run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := CheckFix(tt.before, tt.after)
			if got != tt.want || !strings.Contains(reason, tt.reason) {
				t.Errorf("got %s %q, want %s containing %q", got, reason, tt.want, tt.reason)
			}
		})
	}
}
