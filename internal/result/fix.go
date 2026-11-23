package result

import "fmt"

// FixState is the verdict of a fix check: the same report run on its own
// commit and then on the commit that claims to fix it.
type FixState string

const (
	// Fixed: it crashed on the report commit and no run crashed on the fix
	// commit.
	Fixed FixState = "FIXED"
	// NotFixed: it still crashes on the fix commit, in some or all runs.
	NotFixed FixState = "NOT_FIXED"
	// CannotTell: one of the two runs gave no usable answer, for example
	// the report did not reproduce or the fix commit did not build.
	CannotTell FixState = "CANNOT_TELL"
)

// FixCheck is a fix check: the report run on its own commit and, when it
// reproduced there, on the fix commit. It is what --fix prints as JSON and
// what the service stores and signs.
type FixCheck struct {
	// ReportID is the service report this check is for; empty from the
	// CLI. It is set before signing, like Result.ReportID.
	ReportID string `json:"report_id,omitempty"`
	// FixCommit is the fix commit as it was asked for. FixRun holds the
	// full sha it resolved to.
	FixCommit string   `json:"fix_commit"`
	Fix       FixState `json:"fix"`
	Reason    string   `json:"reason"`
	Report    *Result  `json:"report"`            // on the report commit
	FixRun    *Result  `json:"fix_run,omitempty"` // on the fix commit; absent when the report did not reproduce
}

// Reproduced reports whether every run of r crashed.
func (r *Result) Reproduced() bool {
	return r.State == ReproducedAsClaimed || r.State == ReproducedDifferent
}

// CheckFix compares the run on the report commit (before) with the run on
// the fix commit (after). after may be nil when before did not reproduce,
// because then there is nothing to fix and no need to build the fix.
func CheckFix(before, after *Result) (FixState, string) {
	if !before.Reproduced() {
		return CannotTell, fmt.Sprintf("the report does not reproduce on its own commit (%s), so there is nothing to check", before.State)
	}
	if after == nil {
		return CannotTell, "the fix commit was not run"
	}
	switch after.State {
	case NotReproduced:
		return Fixed, fmt.Sprintf("crashed on %s, no crash on %s", short(before.Inputs.Commit), short(after.Inputs.Commit))
	case ReproducedAsClaimed, ReproducedDifferent, Flaky:
		how := "it still crashes on the fix commit"
		if after.State == Flaky {
			how = fmt.Sprintf("it still crashes on the fix commit in %d of %d runs", after.Crashed, len(after.Runs))
		}
		if before.Crash != nil && after.Crash != nil {
			if before.Crash.Signature == after.Crash.Signature {
				return NotFixed, how + ", with the same crash"
			}
			return NotFixed, fmt.Sprintf("%s, with a different crash (%s)", how, after.Crash.Kind)
		}
		return NotFixed, how
	default:
		return CannotTell, fmt.Sprintf("the fix commit gave %s: %s", after.State, after.Reason)
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
