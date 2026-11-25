package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/source"
)

// history is a fake project: commit i is sha(i), commit 0 is the good
// commit, and the last one is the report commit.
type history struct {
	n        int
	firstBad int                  // first commit with the crash
	answers  map[int]result.State // commits that give another state
	other    map[int]bool         // commits that crash with another bug
	ran      []int
}

func sha(i int) string { return fmt.Sprintf("%040x", i+1) }

func index(s string) int {
	var i int
	fmt.Sscanf(s, "%x", &i)
	return i - 1
}

var bisectCrash = &result.Crash{Kind: "heap-buffer-overflow", Signature: "aaaa", Site: "s1"}

func (h *history) run(_ context.Context, o Options) (*result.Result, error) {
	i := index(o.Commit)
	h.ran = append(h.ran, i)
	r := &result.Result{Inputs: result.Inputs{Commit: o.Commit}, State: result.NotReproduced}
	switch {
	case h.answers[i] != "":
		r.State = h.answers[i]
	case h.other[i]:
		r.State = result.ReproducedDifferent
		r.Crash = &result.Crash{Kind: "use-after-free", Signature: "bbbb"}
	case i >= h.firstBad:
		r.State = result.ReproducedAsClaimed
		r.Crash = bisectCrash
	}
	return r, nil
}

func (h *history) path(_ context.Context, _ Options, good, bad string) (string, []string, error) {
	if good != "v1.0" {
		return "", nil, fmt.Errorf("%w: %q", source.ErrUnknownRef, good)
	}
	var out []string
	for i := 1; i < h.n; i++ {
		out = append(out, sha(i))
	}
	if bad != out[len(out)-1] {
		return "", nil, errors.New("bad is not the report commit")
	}
	return sha(0), out, nil
}

func (h *history) bisect(t *testing.T) *result.Bisect {
	t.Helper()
	before := &result.Result{State: result.ReproducedAsClaimed, Crash: bisectCrash, Inputs: result.Inputs{Commit: sha(h.n - 1)}}
	b, err := Bisect(context.Background(), h.run, h.path, Options{}, before, "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Steps) != len(h.ran) {
		t.Errorf("%d steps for %d runs", len(b.Steps), len(h.ran))
	}
	return b
}

func TestBisectFindsEveryCommit(t *testing.T) {
	for _, n := range []int{2, 3, 4, 17, 1000} {
		for k := 1; k < n; k++ {
			h := &history{n: n, firstBad: k}
			b := h.bisect(t)
			if b.Bisect != result.Found || b.FirstBad != sha(k) || b.LastGood != sha(k-1) {
				t.Fatalf("n=%d k=%d: %s first_bad=%d last_good=%d: %s", n, k, b.Bisect, index(b.FirstBad), index(b.LastGood), b.Reason)
			}
			if b.Commits != n-1 {
				t.Errorf("n=%d: commits = %d", n, b.Commits)
			}
			// The good commit, then at most ceil(log2(n-1)) more.
			limit := 1
			for m := 1; m < n-1; m *= 2 {
				limit++
			}
			if len(h.ran) > limit {
				t.Errorf("n=%d k=%d: %d runs, want at most %d", n, k, len(h.ran), limit)
			}
			if h.ran[0] != 0 || b.Steps[0].Verdict != result.StepGood {
				t.Errorf("n=%d k=%d: the good commit was not run first: %v", n, k, h.ran)
			}
			if k < 3 && n > 3 {
				continue
			}
			if !strings.Contains(b.Reason, sha(k)[:7]) {
				t.Errorf("reason %q does not name the commit", b.Reason)
			}
		}
	}
}

func TestBisectStepsAroundSkips(t *testing.T) {
	// Commits 4 to 6 do not build and the crash starts at 5. 3 is good
	// and 7 is bad, so the first bad commit is one of 4 to 7.
	h := &history{n: 16, firstBad: 5, answers: map[int]result.State{4: result.BuildFailed, 5: result.BuildFailed, 6: result.BuildFailed}}
	b := h.bisect(t)
	if b.Bisect != result.Range || b.LastGood != sha(3) || b.FirstBad != sha(7) || b.Untested != 3 {
		t.Fatalf("got %s last_good=%d first_bad=%d untested=%d: %s", b.Bisect, index(b.LastGood), index(b.FirstBad), b.Untested, b.Reason)
	}
	if !strings.Contains(b.Reason, "gave no answer") {
		t.Errorf("reason = %q", b.Reason)
	}

	// One commit that does not build next to the answer is stepped around.
	h = &history{n: 16, firstBad: 9, answers: map[int]result.State{8: result.Timeout}}
	if b := h.bisect(t); b.Bisect != result.Range || b.LastGood != sha(7) || b.FirstBad != sha(9) {
		t.Errorf("timeout at 8: got %s %d..%d", b.Bisect, index(b.LastGood), index(b.FirstBad))
	}
	h = &history{n: 16, firstBad: 9, answers: map[int]result.State{7: result.BuildFailed}}
	if b := h.bisect(t); b.Bisect != result.Found || b.FirstBad != sha(9) {
		t.Errorf("build failure at 7: got %s %d", b.Bisect, index(b.FirstBad))
	}
}

func TestBisectAnotherCrashIsNotTheBug(t *testing.T) {
	// Commits 2 to 5 crash with another bug before this one starts at 9.
	// Taking them as bad would give commit 2.
	h := &history{n: 32, firstBad: 9, other: map[int]bool{2: true, 3: true, 4: true, 5: true, 15: true, 16: true}}
	b := h.bisect(t)
	if b.Bisect != result.Found || b.FirstBad != sha(9) {
		t.Fatalf("got %s %d: %s", b.Bisect, index(b.FirstBad), b.Reason)
	}
	for _, s := range b.Steps {
		if s.Signature == "bbbb" && s.Verdict != result.StepSkip {
			t.Errorf("another crash at %d gave %s", index(s.Commit), s.Verdict)
		}
	}
}

func TestBisectSameSiteCounts(t *testing.T) {
	// A refactor renamed a caller, so the signature changed, but the
	// crashing line is the same.
	before := &result.Result{State: result.ReproducedAsClaimed, Crash: bisectCrash}
	r := &result.Result{State: result.ReproducedAsClaimed, Crash: &result.Crash{Signature: "cccc", Site: "s1"}}
	if v, _ := result.StepVerdict(before, r); v != result.StepBad {
		t.Errorf("same site: %s", v)
	}
	r.Crash.Site = "s2"
	if v, _ := result.StepVerdict(before, r); v != result.StepSkip {
		t.Errorf("other site: %s", v)
	}
	// Flaky with the same crash in one run is still the bug.
	r = &result.Result{State: result.Flaky, Crash: &result.Crash{Signature: "cccc"}, Runs: []result.RunOutcome{{}, {Crash: bisectCrash}}}
	if v, _ := result.StepVerdict(before, r); v != result.StepBad {
		t.Errorf("flaky with the crash in one run: %s", v)
	}
	for _, s := range []result.State{result.BuildFailed, result.Timeout, result.OOM, result.InvalidInput} {
		if v, _ := result.StepVerdict(before, &result.Result{State: s}); v != result.StepSkip {
			t.Errorf("%s: %s", s, v)
		}
	}
}

func TestBisectStopsAfterMaxRuns(t *testing.T) {
	answers := map[int]result.State{}
	for i := 1; i < 999; i++ {
		answers[i] = result.BuildFailed
	}
	h := &history{n: 1000, firstBad: 500, answers: answers}
	b := h.bisect(t)
	if b.Bisect != result.Range || len(h.ran) != MaxBisectRuns+1 {
		t.Fatalf("got %s after %d runs", b.Bisect, len(h.ran))
	}
	if b.LastGood != sha(0) || b.FirstBad != sha(999) || b.Untested != 998 {
		t.Errorf("range %d..%d untested %d", index(b.LastGood), index(b.FirstBad), b.Untested)
	}
	if !strings.Contains(b.Reason, "stopped after") {
		t.Errorf("reason = %q", b.Reason)
	}
}

func TestBisectCannotStart(t *testing.T) {
	ctx := context.Background()
	report := func() *result.Result {
		return &result.Result{State: result.ReproducedAsClaimed, Crash: bisectCrash, Inputs: result.Inputs{Commit: sha(9)}}
	}
	for _, c := range []struct {
		name   string
		h      *history
		before *result.Result
		good   string
		want   string
		runs   int
	}{
		{"report does not reproduce", &history{n: 10, firstBad: 5}, &result.Result{State: result.NotReproduced}, "v1.0", "does not reproduce", 0},
		{"bad good commit", &history{n: 10, firstBad: 5}, report(), "-x", "not a commit hash", 0},
		{"unknown good commit", &history{n: 10, firstBad: 5}, report(), "v9", "does not exist", 0},
		{"crash on the good commit", &history{n: 10, firstBad: 0}, report(), "v1.0", "already on the good commit", 1},
		{"good commit does not build", &history{n: 10, firstBad: 5, answers: map[int]result.State{0: result.BuildFailed}}, report(), "v1.0", "gave no answer", 1},
	} {
		b, err := Bisect(ctx, c.h.run, c.h.path, Options{}, c.before, c.good)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if b.Bisect != result.NoAnswer || !strings.Contains(b.Reason, c.want) || len(c.h.ran) != c.runs {
			t.Errorf("%s: %s %q after %d runs", c.name, b.Bisect, b.Reason, len(c.h.ran))
		}
	}

	badRange := func(context.Context, Options, string, string) (string, []string, error) {
		return "", nil, fmt.Errorf("%w: not an ancestor", source.ErrBadRange)
	}
	h := &history{n: 10, firstBad: 5}
	if b, err := Bisect(ctx, h.run, badRange, Options{}, report(), "v1.0"); err != nil || b.Bisect != result.NoAnswer || !strings.Contains(b.Reason, "not an ancestor") {
		t.Errorf("bad range: %v %v", b, err)
	}
}

func TestBisectInfraErrors(t *testing.T) {
	ctx := context.Background()
	report := &result.Result{State: result.ReproducedAsClaimed, Crash: bisectCrash, Inputs: result.Inputs{Commit: sha(9)}}
	h := &history{n: 10, firstBad: 5}
	failPath := func(context.Context, Options, string, string) (string, []string, error) {
		return "", nil, errors.New("disk full")
	}
	if _, err := Bisect(ctx, h.run, failPath, Options{}, report, "v1.0"); err == nil {
		t.Error("path error was not returned")
	}
	calls := 0
	failRun := func(ctx context.Context, o Options) (*result.Result, error) {
		calls++
		if calls == 3 {
			return nil, errors.New("docker down")
		}
		return h.run(ctx, o)
	}
	if _, err := Bisect(ctx, failRun, h.path, Options{}, report, "v1.0"); err == nil || !strings.Contains(err.Error(), "docker down") {
		t.Errorf("run error: %v", err)
	}
	nilRun := func(context.Context, Options) (*result.Result, error) { return nil, nil }
	if _, err := Bisect(ctx, nilRun, h.path, Options{}, report, "v1.0"); err == nil {
		t.Error("nil result was not an error")
	}
}

func TestPick(t *testing.T) {
	for _, c := range []struct {
		lo, hi  int
		skipped []int
		want    int
	}{
		{-1, 9, nil, 4},
		{-1, 1, nil, 0},
		{3, 5, nil, 4},
		{3, 5, []int{4}, -1},
		{0, 10, []int{5}, 4},
		{0, 10, []int{5, 4}, 6},
		{0, 10, []int{1, 2, 3, 4, 5, 6, 7, 8}, 9},
	} {
		s := map[int]bool{}
		for _, i := range c.skipped {
			s[i] = true
		}
		if got := pick(c.lo, c.hi, s); got != c.want {
			t.Errorf("pick(%d, %d, %v) = %d, want %d", c.lo, c.hi, c.skipped, got, c.want)
		}
	}
}
