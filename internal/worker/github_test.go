package worker_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/store/storetest"
	"github.com/DCchoudhury15/reprobot/internal/worker"
)

// fakeCommenter fails the first fails calls, then records comments.
type fakeCommenter struct {
	mu    sync.Mutex
	fails int
	calls int
	got   []string
}

func (f *fakeCommenter) Comment(ctx context.Context, inst int64, repo, ghsa, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.fails {
		return errors.New("502 Bad Gateway")
	}
	f.got = append(f.got, strings.Join([]string{repo, ghsa, body}, "|"))
	return nil
}

// A finished GitHub report gets one comment with its verdict, the fix
// check and a link; a failed post is tried again; other reports get none.
func TestNotifierPostsVerdict(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	ref := store.GitHubRef{Installation: 7, Repo: "octo-org/tinyparse", GHSA: "GHSA-8f4m-hccc-8qph"}
	gh, _, _, err := st.CreateReport(ctx, store.ReportInput{Project: "tinyparse", Dir: "/d/gh", FixCommit: "v1.1", GitHub: &ref})
	if err != nil {
		t.Fatal(err)
	}
	plain := newReport(t, st, "tinyparse", "/d/plain")

	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			st := result.ReproducedAsClaimed
			if o.Commit == "v1.1" {
				st = result.NotReproduced
			}
			return &result.Result{State: st, Reason: "reason for " + string(st), Runs: make([]result.RunOutcome, 3)}, nil
		},
		ProjectsDir: "/projects", CacheDir: "/cache", Lease: 10 * time.Second, PollInterval: 20 * time.Millisecond,
	}
	start(t, w)
	waitFor(t, 10*time.Second, "jobs done", func() bool {
		return getReport(t, st, gh).JobState == store.JobDone && getReport(t, st, plain).JobState == store.JobDone
	})

	gc := &fakeCommenter{fails: 1}
	n := &worker.Notifier{Store: st, GitHub: gc, PublicURL: "https://reprobot.example.org/"}
	if !n.Once(ctx) {
		t.Fatal("no comment was due")
	}
	if len(gc.got) != 0 {
		t.Fatalf("first post should fail: %q", gc.got)
	}
	// The failure waits 30 seconds; make it due now.
	if _, err := st.Pool().Exec(ctx, "UPDATE reports SET github_comment_after = now() WHERE id = $1", gh); err != nil {
		t.Fatal(err)
	}
	var lastErr string
	st.Pool().QueryRow(ctx, "SELECT github_comment_error FROM reports WHERE id = $1", gh).Scan(&lastErr)
	if lastErr != "502 Bad Gateway" {
		t.Errorf("stored error %q", lastErr)
	}
	if !n.Once(ctx) || len(gc.got) != 1 {
		t.Fatalf("second try: %q", gc.got)
	}
	repo, ghsa, body := split3(gc.got[0])
	if repo != "octo-org/tinyparse" || ghsa != "GHSA-8f4m-hccc-8qph" {
		t.Errorf("posted to %s %s", repo, ghsa)
	}
	for _, want := range []string{
		"**reprobot: REPRODUCED_AS_CLAIMED**",
		"Fix check on `v1.1`: **FIXED**",
		"Full result: https://reprobot.example.org/reports/" + gh.String(),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}
	if n.Once(ctx) || gc.calls != 2 {
		t.Errorf("another comment was posted (%d calls); the plain report must get none", gc.calls)
	}
	var posted bool
	st.Pool().QueryRow(ctx, "SELECT github_commented_at IS NOT NULL FROM reports WHERE id = $1", gh).Scan(&posted)
	if !posted {
		t.Error("comment not recorded as posted")
	}
}

func split3(s string) (a, b, c string) {
	a, rest, _ := strings.Cut(s, "|")
	b, c, _ = strings.Cut(rest, "|")
	return a, b, c
}

// Serve keeps posting until cancelled.
func TestNotifierServe(t *testing.T) {
	st := storetest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	ref := store.GitHubRef{Installation: 7, Repo: "a/b", GHSA: "GHSA-xxxx-xxxx-xxxx"}
	_, _, _, err := st.CreateReport(ctx, store.ReportInput{Project: "p", Dir: "/d", GitHub: &ref})
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.Claim(ctx, "w", time.Minute)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if err := st.Complete(ctx, job.ID, "w", &result.Result{State: result.NotReproduced}); err != nil {
		t.Fatal(err)
	}
	gc := &fakeCommenter{}
	done := make(chan struct{})
	go func() {
		(&worker.Notifier{Store: st, GitHub: gc, PollInterval: 10 * time.Millisecond}).Serve(ctx)
		close(done)
	}()
	waitFor(t, 5*time.Second, "comment", func() bool { gc.mu.Lock(); defer gc.mu.Unlock(); return len(gc.got) == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
	if strings.Contains(gc.got[0], "Full result") {
		t.Errorf("link without a public URL: %s", gc.got[0])
	}
}
