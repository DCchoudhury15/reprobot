package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/store/storetest"
)

func TestMain(m *testing.M) { os.Exit(storetest.Main(m)) }

func newReport(t *testing.T, st *store.Store, project string) (uuid.UUID, int64) {
	t.Helper()
	id, jobID, created, err := st.CreateReport(context.Background(), store.ReportInput{
		Project: project,
		Dir:     "/data/reports/" + project,
	})
	if err != nil || !created {
		t.Fatalf("CreateReport: created=%v err=%v", created, err)
	}
	return id, jobID
}

type jobRow struct {
	State     string
	Attempts  int
	Owner     string
	Backoff   time.Duration // run_after - updated_at
	LastError string
}

func getJob(t *testing.T, st *store.Store, id int64) jobRow {
	t.Helper()
	var r jobRow
	var backoff float64
	err := st.Pool().QueryRow(context.Background(), `
		SELECT state, attempts, coalesce(lease_owner, ''),
			extract(epoch FROM run_after - updated_at)::float8, coalesce(last_error, '')
		FROM jobs WHERE id = $1`, id,
	).Scan(&r.State, &r.Attempts, &r.Owner, &backoff, &r.LastError)
	if err != nil {
		t.Fatalf("read job %d: %v", id, err)
	}
	r.Backoff = time.Duration(math.Round(backoff*1000)) * time.Millisecond
	return r
}

func exec(t *testing.T, st *store.Store, sql string, args ...any) {
	t.Helper()
	if _, err := st.Pool().Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func count(t *testing.T, st *store.Store, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := st.Pool().QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// claimEventually polls Claim until it returns a job.
func claimEventually(t *testing.T, st *store.Store, worker string, lease time.Duration) *store.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		job, err := st.Claim(context.Background(), worker, lease)
		if err != nil {
			t.Fatalf("Claim(%s): %v", worker, err)
		}
		if job != nil {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("Claim(%s): no job before deadline", worker)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMigrateConcurrentAndRepeated(t *testing.T) {
	dbURL := storetest.NewDatabase(t)
	ctx := context.Background()
	var stores []*store.Store
	for range 4 {
		st, err := store.Open(ctx, dbURL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		stores = append(stores, st)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(stores))
	for i, st := range stores {
		wg.Go(func() { errs[i] = st.Migrate(ctx) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Migrate %d: %v", i, err)
		}
	}
	if err := stores[0].Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if n := count(t, stores[0], "SELECT count(*) FROM schema_migrations"); n != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", n)
	}
}

func TestClaimCompleteConcurrentWorkers(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	const jobs, workers = 30, 8
	reportOf := map[int64]uuid.UUID{}
	for i := range jobs {
		id, jobID := newReport(t, st, fmt.Sprintf("p%d", i))
		reportOf[jobID] = id
	}

	var mu sync.Mutex
	calls := map[int64]int{}
	fakeRun := func(job *store.Job) *result.Result {
		mu.Lock()
		calls[job.ID]++
		mu.Unlock()
		return &result.Result{State: result.NotReproduced, Reason: fmt.Sprint(job.ID)}
	}

	var wg sync.WaitGroup
	errc := make(chan error, workers)
	for w := range workers {
		owner := fmt.Sprintf("w%d", w)
		wg.Go(func() {
			for {
				job, err := st.Claim(ctx, owner, time.Minute)
				if err != nil {
					errc <- err
					return
				}
				if job == nil {
					return
				}
				if err := st.Complete(ctx, job.ID, owner, fakeRun(job)); err != nil {
					errc <- fmt.Errorf("Complete job %d: %w", job.ID, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}

	if len(calls) != jobs {
		t.Errorf("processed %d distinct jobs, want %d", len(calls), jobs)
	}
	for jobID, n := range calls {
		if n != 1 {
			t.Errorf("job %d processed %d times", jobID, n)
		}
	}
	for jobID, reportID := range reportOf {
		rep, err := st.GetReport(ctx, reportID)
		if err != nil {
			t.Fatal(err)
		}
		if rep.JobState != store.JobDone || rep.Attempts != 1 {
			t.Errorf("job %d: state %s attempts %d", jobID, rep.JobState, rep.Attempts)
		}
		if rep.Result == nil || rep.Result.Reason != fmt.Sprint(jobID) {
			t.Errorf("job %d: wrong result %+v", jobID, rep.Result)
		}
	}
}

func TestLeaseExpiryAndFencing(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	reportID, jobID := newReport(t, st, "p")

	a, err := st.Claim(ctx, "A", time.Second)
	if err != nil || a == nil || a.ID != jobID || a.Attempts != 1 {
		t.Fatalf("A Claim = %+v, %v", a, err)
	}
	if j, err := st.Claim(ctx, "B", time.Minute); err != nil || j != nil {
		t.Fatalf("B got a job with a live lease: %+v, %v", j, err)
	}

	// A stops; after its lease expires B takes the job over.
	b := claimEventually(t, st, "B", time.Minute)
	if b.ID != jobID || b.Attempts != 2 {
		t.Fatalf("B Claim = %+v, want job %d attempt 2", b, jobID)
	}
	if !strings.Contains(getJob(t, st, jobID).LastError, "lease of A expired") {
		t.Errorf("last_error does not record the expired lease: %q", getJob(t, st, jobID).LastError)
	}

	if err := st.Heartbeat(ctx, jobID, "A", time.Minute); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("A Heartbeat = %v, want ErrLeaseLost", err)
	}
	if err := st.Complete(ctx, jobID, "A", &result.Result{Reason: "from A"}); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("A Complete = %v, want ErrLeaseLost", err)
	}
	if err := st.Fail(ctx, jobID, "A", "A failed"); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("A Fail = %v, want ErrLeaseLost", err)
	}
	if got := getJob(t, st, jobID); got.State != store.JobRunning || got.Owner != "B" {
		t.Fatalf("after A's writes job = %+v, want running owned by B", got)
	}

	if err := st.Heartbeat(ctx, jobID, "B", time.Minute); err != nil {
		t.Errorf("B Heartbeat: %v", err)
	}
	if err := st.Complete(ctx, jobID, "B", &result.Result{State: result.NotReproduced, Reason: "from B"}); err != nil {
		t.Fatalf("B Complete: %v", err)
	}
	// A done job takes no more writes, not even from the last owner.
	if err := st.Complete(ctx, jobID, "B", &result.Result{Reason: "again"}); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("second B Complete = %v, want ErrLeaseLost", err)
	}
	if err := st.Complete(ctx, jobID, "A", &result.Result{Reason: "from A"}); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("late A Complete = %v, want ErrLeaseLost", err)
	}

	rep, err := st.GetReport(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.JobState != store.JobDone || rep.Result == nil || rep.Result.Reason != "from B" {
		t.Errorf("report = state %s result %+v, want done with B's result", rep.JobState, rep.Result)
	}
}

func TestFailRetriesWithBackoff(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	_, jobID := newReport(t, st, "p")

	wantBackoff := []time.Duration{2 * time.Second, 4 * time.Second}
	for attempt := 1; attempt <= 3; attempt++ {
		exec(t, st, "UPDATE jobs SET run_after = now() WHERE id = $1", jobID)
		job, err := st.Claim(ctx, "w", time.Minute)
		if err != nil || job == nil || job.ID != jobID || job.Attempts != attempt {
			t.Fatalf("attempt %d: Claim = %+v, %v", attempt, job, err)
		}
		msg := fmt.Sprintf("boom %d", attempt)
		if err := st.Fail(ctx, jobID, "w", msg); err != nil {
			t.Fatalf("attempt %d: Fail: %v", attempt, err)
		}
		got := getJob(t, st, jobID)
		if got.Attempts != attempt || got.LastError != msg || got.Owner != "" {
			t.Errorf("attempt %d: job = %+v", attempt, got)
		}
		if attempt < 3 {
			if got.State != store.JobQueued {
				t.Fatalf("attempt %d: state %s, want queued", attempt, got.State)
			}
			if got.Backoff != wantBackoff[attempt-1] {
				t.Errorf("attempt %d: backoff %v, want %v", attempt, got.Backoff, wantBackoff[attempt-1])
			}
			// run_after is in the future, so the job is not handed out yet.
			if j, err := st.Claim(ctx, "other", time.Minute); err != nil || j != nil {
				t.Fatalf("attempt %d: Claim during backoff = %+v, %v", attempt, j, err)
			}
			// The lease ended with Fail, so the same worker cannot fail it twice.
			if err := st.Fail(ctx, jobID, "w", "again"); !errors.Is(err, store.ErrLeaseLost) {
				t.Errorf("attempt %d: second Fail = %v, want ErrLeaseLost", attempt, err)
			}
		} else if got.State != store.JobFailed {
			t.Fatalf("after max attempts: state %s, want failed", got.State)
		}
	}
	exec(t, st, "UPDATE jobs SET run_after = now() WHERE id = $1", jobID)
	if j, err := st.Claim(ctx, "w", time.Minute); err != nil || j != nil {
		t.Errorf("Claim of a failed job = %+v, %v", j, err)
	}
}

func TestFailBackoffCap(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	_, jobID := newReport(t, st, "p")
	job := claimEventually(t, st, "w", time.Minute)
	exec(t, st, "UPDATE jobs SET attempts = 12, max_attempts = 20 WHERE id = $1", job.ID)
	if err := st.Fail(ctx, jobID, "w", "boom"); err != nil {
		t.Fatal(err)
	}
	if got := getJob(t, st, jobID); got.State != store.JobQueued || got.Backoff != 5*time.Minute {
		t.Errorf("job = %+v, want queued with 5m backoff", got)
	}
}

func TestFailStoresCleanErrorText(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	_, jobID := newReport(t, st, "p")
	claimEventually(t, st, "w", time.Minute)
	long := "bad\x00byte \xff " + strings.Repeat("é", 5000)
	if err := st.Fail(ctx, jobID, "w", long); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	got := getJob(t, st, jobID).LastError
	if len(got) > 4000 || !utf8.ValidString(got) || strings.Contains(got, "\x00") || !strings.HasPrefix(got, "bad") {
		t.Errorf("stored error: len %d valid %v prefix %q", len(got), utf8.ValidString(got), got[:min(len(got), 10)])
	}
}

func TestExpiredLeaseAtMaxAttemptsBecomesFailed(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	_, jobID := newReport(t, st, "p")

	for attempt := 1; attempt <= 3; attempt++ {
		job := claimEventually(t, st, fmt.Sprintf("w%d", attempt), 50*time.Millisecond)
		if job.ID != jobID || job.Attempts != attempt {
			t.Fatalf("claim %d = %+v", attempt, job)
		}
	}
	time.Sleep(100 * time.Millisecond) // let the last lease expire
	_, otherJob := newReport(t, st, "q")

	// The expired job is out of attempts: Claim must fail it and hand out
	// the next job instead.
	job, err := st.Claim(ctx, "w4", time.Minute)
	if err != nil || job == nil || job.ID != otherJob {
		t.Fatalf("Claim = %+v, %v; want job %d", job, err, otherJob)
	}
	got := getJob(t, st, jobID)
	if got.State != store.JobFailed || got.Attempts != 3 || !strings.Contains(got.LastError, "expired") {
		t.Errorf("expired job = %+v, want failed after 3 attempts", got)
	}
	if err := st.Complete(ctx, jobID, "w3", &result.Result{}); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("Complete by last owner = %v, want ErrLeaseLost", err)
	}
	if j, err := st.Claim(ctx, "w5", time.Minute); err != nil || j != nil {
		t.Errorf("Claim = %+v, %v; want no work", j, err)
	}
}

func TestCreateReportIdempotent(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()

	in := store.ReportInput{Project: "p", Dir: "/d/1", IdempotencyKey: "k1"}
	id1, job1, created1, err := st.CreateReport(ctx, in)
	if err != nil || !created1 {
		t.Fatalf("first: created=%v err=%v", created1, err)
	}
	in.Dir = "/d/2"
	in.ID = uuid.New()
	id2, job2, created2, err := st.CreateReport(ctx, in)
	if err != nil || created2 || id2 != id1 || job2 != job1 {
		t.Fatalf("second: %v %d created=%v err=%v; want %v %d", id2, job2, created2, err, id1, job1)
	}
	if rep, _ := st.GetReport(ctx, id1); rep == nil || rep.Dir != "/d/1" {
		t.Errorf("first report changed: %+v", rep)
	}

	// Many requests with one new key at the same time.
	const n = 16
	type out struct {
		id      uuid.UUID
		job     int64
		created bool
		err     error
	}
	outs := make([]out, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			var o out
			o.id, o.job, o.created, o.err = st.CreateReport(ctx, store.ReportInput{
				ID: uuid.New(), Project: "p", Dir: fmt.Sprintf("/d/c%d", i), IdempotencyKey: "k2",
			})
			outs[i] = o
		})
	}
	close(start)
	wg.Wait()
	createdCount := 0
	for i, o := range outs {
		if o.err != nil {
			t.Fatalf("concurrent %d: %v", i, o.err)
		}
		if o.created {
			createdCount++
		}
		if o.id != outs[0].id || o.job != outs[0].job {
			t.Errorf("concurrent %d: ids %v/%d differ from %v/%d", i, o.id, o.job, outs[0].id, outs[0].job)
		}
	}
	if createdCount != 1 {
		t.Errorf("created=true %d times, want 1", createdCount)
	}

	// Without a key every call is a new report.
	newReport(t, st, "p")
	newReport(t, st, "p")

	if got := count(t, st, "SELECT count(*) FROM reports"); got != 4 {
		t.Errorf("reports = %d, want 4", got)
	}
	if got := count(t, st, "SELECT count(*) FROM jobs"); got != 4 {
		t.Errorf("jobs = %d, want 4", got)
	}
	if got := count(t, st, "SELECT count(*) FROM jobs j JOIN reports r ON r.id = j.report_id WHERE r.idempotency_key = 'k2'"); got != 1 {
		t.Errorf("jobs for k2 = %d, want 1", got)
	}
}

// finish runs the job of reportID to completion with res.
func finish(t *testing.T, st *store.Store, reportID uuid.UUID, res *result.Result) {
	t.Helper()
	job := claimEventually(t, st, "w", time.Minute)
	if job.ReportID != reportID {
		t.Fatalf("claimed job for %v, want %v", job.ReportID, reportID)
	}
	if err := st.Complete(context.Background(), job.ID, "w", res); err != nil {
		t.Fatal(err)
	}
}

func crashResult(signature string) *result.Result {
	return &result.Result{
		State: result.ReproducedAsClaimed,
		Crash: &result.Crash{Kind: "heap-buffer-overflow", Signature: signature},
	}
}

func TestDuplicates(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()

	a, _ := newReport(t, st, "p1")
	finish(t, st, a, crashResult("sig-1"))
	b, _ := newReport(t, st, "p1")
	finish(t, st, b, crashResult("sig-1"))
	c, _ := newReport(t, st, "p2") // same signature, other project
	finish(t, st, c, crashResult("sig-1"))
	d, _ := newReport(t, st, "p1") // other signature
	finish(t, st, d, crashResult("sig-2"))
	e1, _ := newReport(t, st, "p1") // empty signatures
	finish(t, st, e1, crashResult(""))
	e2, _ := newReport(t, st, "p1")
	finish(t, st, e2, crashResult(""))
	n1, _ := newReport(t, st, "p1") // no crash at all
	finish(t, st, n1, &result.Result{State: result.NotReproduced})

	check := func(project, sig string, exclude uuid.UUID, want ...uuid.UUID) {
		t.Helper()
		got, err := st.Duplicates(ctx, project, sig, exclude)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("Duplicates(%s, %q, %v) = %v, want %v", project, sig, exclude, got, want)
		}
	}
	check("p1", "sig-1", a, b)
	check("p1", "sig-1", b, a)
	check("p2", "sig-1", c)
	check("p1", "sig-2", d)
	check("p1", "", e1)
	check("p1", "", n1)

	rep, err := st.GetReport(ctx, a)
	if err != nil || rep.Signature != "sig-1" {
		t.Errorf("GetReport(a) signature = %q, %v", rep.Signature, err)
	}
	if n := count(t, st, "SELECT count(*) FROM results WHERE signature IS NOT NULL AND signature = ''"); n != 0 {
		t.Errorf("%d results store an empty signature instead of NULL", n)
	}
}

func TestCompleteStoresOutputWithNUL(t *testing.T) {
	st := storetest.New(t)
	ctx := context.Background()
	id, _ := newReport(t, st, "p")
	res := &result.Result{
		State:  result.ReproducedAsClaimed,
		Reason: `a literal \u0000 stays`,
		Runs:   []result.RunOutcome{{ExitCode: 1, StderrTail: "x\x00y"}},
	}
	finish(t, st, id, res)
	rep, err := st.GetReport(ctx, id)
	if err != nil || rep.Result == nil {
		t.Fatalf("GetReport: %+v, %v", rep, err)
	}
	if rep.Result.Reason != `a literal \u0000 stays` {
		t.Errorf("reason = %q", rep.Result.Reason)
	}
	if got := rep.Result.Runs[0].StderrTail; got != "x�y" {
		t.Errorf("stderr tail = %q, want NUL replaced", got)
	}
}

func TestGetReportNotFound(t *testing.T) {
	st := storetest.New(t)
	if _, err := st.GetReport(context.Background(), uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetReport = %v, want ErrNotFound", err)
	}
}
