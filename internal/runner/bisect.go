package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/source"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// MaxBisectRuns caps the commits a bisect runs after the good commit.
// Each run may be a fresh build, so a long run of commits that do not
// build stops here and gives a RANGE.
const MaxBisectRuns = 24

// PathFunc returns the full sha of good and the commits after it up to
// and including bad, oldest first. An error that wraps source.ErrBadRange
// or source.ErrUnknownRef is bad input; any other is an infrastructure
// error.
type PathFunc func(ctx context.Context, o Options, good, bad string) (goodSHA string, commits []string, err error)

// CommitPath is the real PathFunc: it resolves good in the project's
// mirror and walks the first-parent history from it to bad.
func CommitPath(ctx context.Context, o Options, good, bad string) (string, []string, error) {
	p, err := spec.LoadProject(o.ProjectPath)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", source.ErrBadRange, err)
	}
	cacheDir, err := filepath.Abs(o.CacheDir)
	if err != nil {
		return "", nil, err
	}
	mirror, goodSHA, err := source.Resolve(ctx, cacheDir, p.Repo, good)
	if err != nil {
		return "", nil, err
	}
	commits, err := source.FirstParentPath(ctx, mirror, goodSHA, bad)
	return goodSHA, commits, err
}

// Bisect finds the first commit where the report's crash appears, between
// good (a commit without it) and the report commit, where before (the run
// on the report commit) found it. It first checks that the crash is not
// on good, then halves the first-parent history, stepping around commits
// that give no answer. An error is an infrastructure error. run is Run and
// path is CommitPath, or fakes in tests.
func Bisect(ctx context.Context, run func(context.Context, Options) (*result.Result, error), path PathFunc, o Options, before *result.Result, good string) (*result.Bisect, error) {
	b := &result.Bisect{GoodCommit: good, Report: before, Bisect: result.NoAnswer}
	if !before.Reproduced() {
		b.Reason = fmt.Sprintf("the report does not reproduce on its own commit (%s), so there is nothing to look for", before.State)
		return b, nil
	}
	if !spec.ValidCommit(good) {
		b.Reason = fmt.Sprintf("good commit %q is not a commit hash or tag name", good)
		return b, nil
	}
	goodSHA, commits, err := path(ctx, o, good, before.Inputs.Commit)
	switch {
	case errors.Is(err, source.ErrUnknownRef):
		b.Reason = fmt.Sprintf("good commit %q does not exist in the project repo", good)
		return b, nil
	case errors.Is(err, source.ErrBadRange):
		b.Reason = err.Error()
		return b, nil
	case err != nil:
		return nil, fmt.Errorf("bisect: %w", err)
	case len(commits) == 0 || commits[len(commits)-1] != before.Inputs.Commit:
		return nil, fmt.Errorf("bisect: the commit path does not end at the report commit")
	}
	b.Commits = len(commits)

	step := func(sha string) (string, error) {
		o.Commit = sha
		r, err := run(ctx, o)
		if err != nil {
			return "", fmt.Errorf("bisect at %s: %w", short(sha), err)
		}
		if r == nil {
			return "", fmt.Errorf("bisect at %s: runner returned no result", short(sha))
		}
		v, why := result.StepVerdict(before, r)
		s := result.BisectStep{Commit: sha, Verdict: v, State: r.State, Reason: why}
		if r.Crash != nil {
			s.Signature = r.Crash.Signature
		}
		b.Steps = append(b.Steps, s)
		return v, nil
	}

	switch v, err := step(goodSHA); {
	case err != nil:
		return nil, err
	case v == result.StepBad:
		b.Reason = fmt.Sprintf("the same crash is already on the good commit %s; pick an older one", short(goodSHA))
		return b, nil
	case v == result.StepSkip:
		b.Reason = fmt.Sprintf("the good commit %s gave no answer: %s", short(goodSHA), b.Steps[0].Reason)
		return b, nil
	}

	// commits[lo] is good (lo == -1 is the good commit), commits[hi] is
	// bad. Skipped commits stay between them.
	lo, hi := -1, len(commits)-1
	skipped := map[int]bool{}
	runs := 0
	for hi-lo > 1 {
		i := pick(lo, hi, skipped)
		if i < 0 {
			break
		}
		if runs == MaxBisectRuns {
			break
		}
		runs++
		v, err := step(commits[i])
		if err != nil {
			return nil, err
		}
		switch v {
		case result.StepGood:
			lo = i
		case result.StepBad:
			hi = i
		default:
			skipped[i] = true
		}
	}
	b.FirstBad = commits[hi]
	b.LastGood = goodSHA
	if lo >= 0 {
		b.LastGood = commits[lo]
	}
	if hi-lo == 1 {
		b.Bisect = result.Found
		b.Reason = fmt.Sprintf("the crash first appears in %s; its parent %s has no crash", short(b.FirstBad), short(b.LastGood))
		return b, nil
	}
	b.Bisect = result.Range
	b.Untested = hi - lo - 1
	why := "the commits between them gave no answer"
	if runs == MaxBisectRuns && pick(lo, hi, skipped) >= 0 {
		why = fmt.Sprintf("the bisect stopped after %d runs", MaxBisectRuns)
	}
	b.Reason = fmt.Sprintf("the crash first appears after %s and no later than %s (%d commits between them not told apart); %s",
		short(b.LastGood), short(b.FirstBad), b.Untested, why)
	return b, nil
}

// pick returns the untested commit between lo and hi nearest the middle,
// or -1 when every one was skipped.
func pick(lo, hi int, skipped map[int]bool) int {
	mid := lo + (hi-lo)/2
	for d := 0; mid-d > lo || mid+d < hi; d++ {
		if i := mid - d; i > lo && !skipped[i] {
			return i
		}
		if i := mid + d; i < hi && !skipped[i] {
			return i
		}
	}
	return -1
}
