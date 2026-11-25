package result

import "fmt"

// BisectState is the verdict of a bisect: the report run on the commits
// between a commit without the bug and the report's own commit, to find
// the first commit with the crash.
type BisectState string

const (
	// Found: FirstBad is the first commit with the crash. LastGood, its
	// parent, ran without it.
	Found BisectState = "FOUND"
	// Range: commits that could not be tested (they did not build, or
	// gave another crash) hide the exact commit. The first commit with the
	// crash is after LastGood and no later than FirstBad.
	Range BisectState = "RANGE"
	// NoAnswer: the bisect could not start, for example the report did not
	// reproduce, or it also crashes on the good commit.
	NoAnswer BisectState = "CANNOT_TELL"
)

// Step verdicts.
const (
	StepGood = "good" // ran, no crash
	StepBad  = "bad"  // ran, the same crash as on the report commit
	StepSkip = "skip" // no usable answer: did not build, timed out, another crash
)

// BisectStep is one commit the bisect ran.
type BisectStep struct {
	Commit    string `json:"commit"`
	Verdict   string `json:"verdict"` // good | bad | skip
	State     State  `json:"state"`
	Reason    string `json:"reason"`
	Signature string `json:"signature,omitempty"` // of the crash, when it crashed
}

// Bisect is a bisect: what --bisect prints as JSON and what the service
// stores and signs.
type Bisect struct {
	// ReportID is the service report this bisect is for; empty from the
	// CLI. It is set before signing.
	ReportID string `json:"report_id,omitempty"`
	// GoodCommit is the good commit as it was asked for. The first step
	// holds the full sha it resolved to.
	GoodCommit string      `json:"good_commit"`
	Bisect     BisectState `json:"bisect"`
	Reason     string      `json:"reason"`
	// LastGood and FirstBad are set for FOUND and RANGE.
	LastGood string `json:"last_good,omitempty"`
	FirstBad string `json:"first_bad,omitempty"`
	// Untested counts the commits in a RANGE that were not told apart:
	// the first bad commit is one of them or FirstBad.
	Untested int `json:"untested,omitempty"`
	// Commits is the number of commits after the good commit up to the
	// report commit, on its first-parent history.
	Commits int          `json:"commits,omitempty"`
	Report  *Result      `json:"report"`          // on the report commit
	Steps   []BisectStep `json:"steps,omitempty"` // in the order they ran, the good commit first
}

// StepVerdict says what the run r on another commit tells about the bug
// that report (the run on the report commit) found. Bad needs the same
// crash, by signature or by site: a bisect looks for this bug, not for any
// crash. Any other crash, or no answer at all, is a skip.
func StepVerdict(report, r *Result) (verdict, why string) {
	switch r.State {
	case NotReproduced:
		return StepGood, "no crash"
	case ReproducedAsClaimed, ReproducedDifferent, Flaky:
		if SameBug(report.Crash, r) {
			return StepBad, "the same crash"
		}
		kind := "?"
		if r.Crash != nil {
			kind = r.Crash.Kind
		}
		return StepSkip, fmt.Sprintf("another crash (%s), so this commit cannot tell", kind)
	default:
		return StepSkip, fmt.Sprintf("%s (%s), so this commit cannot tell", r.State, r.Reason)
	}
}

// SameBug reports whether a run of r hit crash c: the same signature in
// any run, or the same site as the crash r settled on.
func SameBug(c *Crash, r *Result) bool {
	if c == nil {
		return false
	}
	if r.Crash != nil && (r.Crash.Signature == c.Signature || (c.Site != "" && r.Crash.Site == c.Site)) {
		return true
	}
	for _, run := range r.Runs {
		if run.Crash != nil && run.Crash.Signature == c.Signature {
			return true
		}
	}
	return false
}
