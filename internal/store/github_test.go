package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/store/storetest"
)

var ghRef = store.GitHubRef{Installation: 52219614, Repo: "octo-org/tinyparse", GHSA: "GHSA-8f4m-hccc-8qph"}

// finishGitHubReport creates a GitHub report and completes its job.
func finishGitHubReport(t *testing.T, st *store.Store, ref store.GitHubRef, fix *result.FixCheck) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, _, created, err := st.CreateReport(ctx, store.ReportInput{
		Project: "tinyparse", Dir: "/data/reports/" + ref.GHSA, IdempotencyKey: "github:" + ref.GHSA, GitHub: &ref,
	})
	if err != nil || !created {
		t.Fatalf("CreateReport: %v %v", created, err)
	}
	job, err := st.Claim(ctx, "w", time.Minute)
	if err != nil || job == nil || job.ReportID != id {
		t.Fatalf("Claim: %+v %v", job, err)
	}
	res := &result.Result{State: result.ReproducedAsClaimed, Reason: "crashed in 3 of 3 runs, as claimed", Crashed: 3}
	if err := st.CompleteJob(ctx, job.ID, "w", store.Outcome{Result: res, Fix: fix}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestGitHubCommentQueue(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()

	if c, err := st.ClaimGitHubComment(ctx, time.Minute); err != nil || c != nil {
		t.Fatalf("empty queue: %+v %v", c, err)
	}
	fix := &result.FixCheck{FixCommit: "v1.1", Fix: result.Fixed}
	id := finishGitHubReport(t, st, ghRef, fix)
	// A report that is not from GitHub, and a GitHub report still queued,
	// are never offered.
	newReport(t, st, "tinyparse")
	if _, _, _, err := st.CreateReport(ctx, store.ReportInput{
		Project: "tinyparse", Dir: "/d/queued", GitHub: &store.GitHubRef{Installation: 1, Repo: "a/b", GHSA: "GHSA-xxxx-xxxx-xxxx"},
	}); err != nil {
		t.Fatal(err)
	}
	c, err := st.ClaimGitHubComment(ctx, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("Claim: %+v %v", c, err)
	}
	if c.ReportID != id || c.Ref != ghRef || c.Attempts != 1 || c.Result.State != result.ReproducedAsClaimed ||
		c.Fix == nil || c.Fix.Fix != result.Fixed {
		t.Errorf("claimed %+v", c)
	}
	// Held: nobody else gets it.
	if c2, err := st.ClaimGitHubComment(ctx, time.Minute); err != nil || c2 != nil {
		t.Errorf("held comment claimed again: %+v %v", c2, err)
	}

	// A failure makes it due again after the retry time.
	if err := st.GitHubCommentFailed(ctx, id, 0, "502 Bad Gateway"); err != nil {
		t.Fatal(err)
	}
	c, err = st.ClaimGitHubComment(ctx, time.Minute)
	if err != nil || c == nil || c.Attempts != 2 || c.Fix == nil {
		t.Fatalf("retry: %+v %v", c, err)
	}
	if got := count(t, st, "SELECT count(*) FROM reports WHERE github_comment_error = '502 Bad Gateway'"); got != 1 {
		t.Errorf("error not stored")
	}

	// Once posted it is done for good.
	if err := st.GitHubCommented(ctx, id); err != nil {
		t.Fatal(err)
	}
	exec(t, st, "UPDATE reports SET github_comment_after = NULL")
	if c, err := st.ClaimGitHubComment(ctx, time.Minute); err != nil || c != nil {
		t.Errorf("posted comment offered again: %+v %v", c, err)
	}
}

func TestGitHubCommentGivesUp(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	id := finishGitHubReport(t, st, ghRef, nil)
	for i := 1; i <= store.MaxGitHubCommentAttempts; i++ {
		c, err := st.ClaimGitHubComment(ctx, time.Minute)
		if err != nil || c == nil || c.Attempts != i || c.Fix != nil {
			t.Fatalf("attempt %d: %+v %v", i, c, err)
		}
		if err := st.GitHubCommentFailed(ctx, id, 0, "down"); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := st.ClaimGitHubComment(ctx, time.Minute); err != nil || c != nil {
		t.Errorf("offered after %d attempts: %+v %v", store.MaxGitHubCommentAttempts, c, err)
	}
}

// Two notifiers never get the same comment.
func TestGitHubCommentClaimOnce(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	refs := []store.GitHubRef{ghRef, {Installation: 1, Repo: "a/b", GHSA: "GHSA-xxxx-xxxx-xxxx"}, {Installation: 1, Repo: "a/b", GHSA: "GHSA-cccc-cccc-cccc"}}
	for _, r := range refs {
		finishGitHubReport(t, st, r, nil)
	}
	seen := make(chan uuid.UUID, 20)
	done := make(chan struct{})
	for range 6 {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				c, err := st.ClaimGitHubComment(ctx, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if c == nil {
					return
				}
				seen <- c.ReportID
			}
		}()
	}
	for range 6 {
		<-done
	}
	close(seen)
	got := map[uuid.UUID]int{}
	for id := range seen {
		got[id]++
	}
	if len(got) != 3 {
		t.Errorf("claimed %d reports, want 3", len(got))
	}
	for id, n := range got {
		if n != 1 {
			t.Errorf("report %s claimed %d times", id, n)
		}
	}
}
