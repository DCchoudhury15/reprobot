package worker_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/sign"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/store/storetest"
	"github.com/DCchoudhury15/reprobot/internal/worker"
)

func TestMain(m *testing.M) { os.Exit(storetest.Main(m)) }

func newReport(t *testing.T, st *store.Store, project, dir string) uuid.UUID {
	t.Helper()
	id, _, _, err := st.CreateReport(context.Background(), store.ReportInput{Project: project, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func getReport(t *testing.T, st *store.Store, id uuid.UUID) *store.Report {
	t.Helper()
	rep, err := st.GetReport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// start runs w.Serve in the background. stop cancels it and waits for Serve
// to return; it fails the test if that takes too long.
func start(t *testing.T, w *worker.Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Serve(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Serve: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Errorf("Serve did not return after cancel")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestWorkerStoresResults(t *testing.T) {
	st := storetest.New(t)
	var calls atomic.Int32
	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			calls.Add(1)
			return &result.Result{
				State:  result.NotReproduced,
				Reason: strings.Join([]string{o.ProjectPath, o.ReproPath, o.CacheDir}, "|"),
			}, nil
		},
		ProjectsDir:  "/projects",
		CacheDir:     "/cache",
		Concurrency:  3,
		Lease:        10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	var ids []uuid.UUID
	for i := range 5 {
		ids = append(ids, newReport(t, st, fmt.Sprintf("proj%d", i), fmt.Sprintf("/data/reports/r%d", i)))
	}
	start(t, w)

	waitFor(t, 10*time.Second, "all jobs done", func() bool {
		for _, id := range ids {
			if getReport(t, st, id).JobState != store.JobDone {
				return false
			}
		}
		return true
	})
	for i, id := range ids {
		rep := getReport(t, st, id)
		want := fmt.Sprintf("/projects/proj%d.yaml|/data/reports/r%d/repro.yaml|/cache", i, i)
		if rep.Result == nil || rep.Result.Reason != want || rep.Attempts != 1 {
			t.Errorf("report %d: attempts %d result %+v, want reason %q", i, rep.Attempts, rep.Result, want)
		}
	}
	if n := calls.Load(); n != 5 {
		t.Errorf("Run called %d times, want 5", n)
	}
}

func TestWorkerRetriesAfterRunError(t *testing.T) {
	st := storetest.New(t)
	var calls atomic.Int32
	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("docker is down")
			}
			return &result.Result{State: result.NotReproduced, Reason: "second try"}, nil
		},
		ProjectsDir:  "/projects",
		Lease:        10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	id := newReport(t, st, "p", "/data/reports/r")
	start(t, w)

	waitFor(t, 5*time.Second, "first failure", func() bool { return getReport(t, st, id).LastError != "" })
	if rep := getReport(t, st, id); rep.JobState != store.JobQueued || rep.Attempts != 1 || rep.LastError != "docker is down" {
		t.Fatalf("after first run: state %s attempts %d last_error %q", rep.JobState, rep.Attempts, rep.LastError)
	}
	waitFor(t, 15*time.Second, "retry to finish", func() bool { return getReport(t, st, id).JobState == store.JobDone })
	rep := getReport(t, st, id)
	if rep.Attempts != 2 || rep.Result == nil || rep.Result.Reason != "second try" {
		t.Errorf("after retry: attempts %d result %+v", rep.Attempts, rep.Result)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("Run called %d times, want 2", n)
	}
}

func TestWorkerStopsOnCancel(t *testing.T) {
	st := storetest.New(t)
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			calls.Add(1)
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
		ProjectsDir:  "/projects",
		Concurrency:  4,
		Lease:        10 * time.Second,
		PollInterval: 20 * time.Millisecond,
	}
	id := newReport(t, st, "p", "/data/reports/r")
	stop := start(t, w)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not start")
	}
	// One loop is inside Run, three are polling an empty queue.
	stop()
	if t.Failed() {
		return
	}

	rep := getReport(t, st, id)
	if rep.JobState != store.JobQueued || rep.Attempts != 1 || !strings.Contains(rep.LastError, "worker stopped") {
		t.Errorf("interrupted job: state %s attempts %d last_error %q", rep.JobState, rep.Attempts, rep.LastError)
	}
	// No loop is left behind to take new work.
	other := newReport(t, st, "p", "/data/reports/other")
	time.Sleep(300 * time.Millisecond)
	if rep := getReport(t, st, other); rep.JobState != store.JobQueued || rep.Attempts != 0 {
		t.Errorf("job taken after Serve returned: state %s attempts %d", rep.JobState, rep.Attempts)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("Run called %d times, want 1", n)
	}
}

func TestWorkerHeartbeatKeepsLease(t *testing.T) {
	st := storetest.New(t)
	const lease = 600 * time.Millisecond
	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			select {
			case <-time.After(4 * lease):
				return &result.Result{State: result.NotReproduced}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		ProjectsDir:  "/projects",
		Lease:        lease,
		PollInterval: 20 * time.Millisecond,
	}
	id := newReport(t, st, "p", "/data/reports/r")
	start(t, w)
	waitFor(t, 5*time.Second, "job to start", func() bool { return getReport(t, st, id).JobState == store.JobRunning })

	// Well past the first lease, the job must still be leased to the worker.
	time.Sleep(2 * lease)
	job, err := st.Claim(context.Background(), "intruder", time.Minute)
	if err != nil || job != nil {
		t.Fatalf("intruder claimed a job that has a heartbeat: %+v, %v", job, err)
	}
	waitFor(t, 10*time.Second, "job done", func() bool { return getReport(t, st, id).JobState == store.JobDone })
	if rep := getReport(t, st, id); rep.Attempts != 1 || rep.Result == nil {
		t.Errorf("attempts %d result %+v", rep.Attempts, rep.Result)
	}
}

func TestWorkerLeaseLostCancelsRun(t *testing.T) {
	st := storetest.New(t)
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	w := &worker.Worker{
		Store: st,
		Run: func(ctx context.Context, o runner.Options) (*result.Result, error) {
			started <- struct{}{}
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
				return nil, ctx.Err()
			case <-time.After(30 * time.Second):
				return &result.Result{State: result.NotReproduced}, nil
			}
		},
		ProjectsDir:  "/projects",
		Lease:        600 * time.Millisecond,
		PollInterval: 20 * time.Millisecond,
	}
	id := newReport(t, st, "p", "/data/reports/r")
	start(t, w)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("job did not start")
	}

	// Another worker takes the lease over.
	if _, err := st.Pool().Exec(context.Background(),
		"UPDATE jobs SET lease_owner = 'intruder' WHERE state = 'running'"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("run was not cancelled after the lease was lost")
	}
	// The worker must not write anything for a job it no longer owns.
	time.Sleep(200 * time.Millisecond)
	rep := getReport(t, st, id)
	if rep.JobState != store.JobRunning || rep.Result != nil || rep.LastError != "" {
		t.Errorf("job after lease loss: state %s last_error %q result %+v", rep.JobState, rep.LastError, rep.Result)
	}
}

func TestWorkerSignsResults(t *testing.T) {
	st := storetest.New(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// The NUL byte is rewritten in the stored jsonb result, so this also
	// checks that the envelope is kept byte for byte, apart from it.
	run := func(ctx context.Context, o runner.Options) (*result.Result, error) {
		return &result.Result{State: result.ReproducedAsClaimed, Runs: []result.RunOutcome{{Report: "bad\x00byte"}}}, nil
	}
	newWorker := func(key ed25519.PrivateKey) *worker.Worker {
		return &worker.Worker{Store: st, Run: run, ProjectsDir: "/p", CacheDir: "/c",
			Lease: 10 * time.Second, PollInterval: 20 * time.Millisecond, SignKey: key}
	}
	finished := func(id uuid.UUID) *store.Report {
		t.Helper()
		waitFor(t, 10*time.Second, "job done", func() bool { return getReport(t, st, id).JobState == store.JobDone })
		return getReport(t, st, id)
	}

	signed := newReport(t, st, "p", "/d/1")
	stop := start(t, newWorker(priv))
	rep := finished(signed)
	stop()
	if rep.Envelope == nil {
		t.Fatal("no envelope stored")
	}
	env, err := sign.Decode(rep.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := sign.Verify(env, pub)
	if err != nil {
		t.Fatalf("stored envelope does not verify: %v", err)
	}
	var got result.Result
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.ReportID != signed.String() || got.State != result.ReproducedAsClaimed || got.Runs[0].Report != "bad\x00byte" {
		t.Errorf("signed payload: report %q state %s run report %q", got.ReportID, got.State, got.Runs[0].Report)
	}
	if rep.Result.ReportID != signed.String() {
		t.Errorf("stored result report_id %q", rep.Result.ReportID)
	}

	unsigned := newReport(t, st, "p", "/d/2")
	start(t, newWorker(nil))
	if rep := finished(unsigned); rep.Envelope != nil {
		t.Errorf("worker without a key stored an envelope: %s", rep.Envelope)
	}
}

func TestWorkerFixCheck(t *testing.T) {
	st := storetest.New(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	crash := &result.Crash{Kind: "heap-buffer-overflow", Signature: "A"}
	var mu sync.Mutex
	commits := map[string][]string{} // repro path -> commits run, "" = the report's own
	failFixOnce := map[string]bool{"/d/flaky-infra/repro.yaml": true}
	run := func(ctx context.Context, o runner.Options) (*result.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		commits[o.ReproPath] = append(commits[o.ReproPath], o.Commit)
		if o.Commit == "" {
			state := result.ReproducedAsClaimed
			if strings.Contains(o.ReproPath, "no-repro") {
				state = result.NotReproduced
			}
			return &result.Result{State: state, Crash: crash, Inputs: result.Inputs{Commit: "aaaaaaaaaa"}}, nil
		}
		if failFixOnce[o.ReproPath] {
			failFixOnce[o.ReproPath] = false
			return nil, errors.New("docker is down")
		}
		return &result.Result{State: result.NotReproduced, Inputs: result.Inputs{Commit: "bbbbbbbbbb"}}, nil
	}
	create := func(dir string) uuid.UUID {
		id, _, _, err := st.CreateReport(context.Background(), store.ReportInput{Project: "p", Dir: dir, FixCommit: "bbbbbbb"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	fixed, noRepro, flakyInfra := create("/d/fixed"), create("/d/no-repro"), create("/d/flaky-infra")
	start(t, &worker.Worker{Store: st, Run: run, ProjectsDir: "/p", CacheDir: "/c",
		Lease: 10 * time.Second, PollInterval: 20 * time.Millisecond, SignKey: priv})
	for _, id := range []uuid.UUID{fixed, noRepro, flakyInfra} {
		waitFor(t, 15*time.Second, "job done", func() bool { return getReport(t, st, id).JobState == store.JobDone })
	}

	// The fix commit was run after the report commit, and only when the
	// report reproduced.
	mu.Lock()
	if got := commits["/d/fixed/repro.yaml"]; len(got) != 2 || got[0] != "" || got[1] != "bbbbbbb" {
		t.Errorf("fixed: commits run %q", got)
	}
	if got := commits["/d/no-repro/repro.yaml"]; len(got) != 1 {
		t.Errorf("no-repro: commits run %q, want only the report's", got)
	}
	// An infrastructure error on the fix run retries the whole job.
	if got := commits["/d/flaky-infra/repro.yaml"]; len(got) != 4 {
		t.Errorf("flaky-infra: commits run %q, want report+fix twice", got)
	}
	mu.Unlock()
	if rep := getReport(t, st, flakyInfra); rep.Attempts != 2 || rep.Fix == nil || rep.Fix.Fix != result.Fixed {
		t.Errorf("flaky-infra: attempts %d fix %+v", rep.Attempts, rep.Fix)
	}
	if rep := getReport(t, st, noRepro); rep.Fix == nil || rep.Fix.Fix != result.CannotTell || rep.Fix.FixRun != nil {
		t.Errorf("no-repro: fix %+v", rep.Fix)
	}

	rep := getReport(t, st, fixed)
	if rep.Fix == nil || rep.Fix.Fix != result.Fixed || rep.Fix.FixCommit != "bbbbbbb" {
		t.Fatalf("fixed: fix %+v", rep.Fix)
	}
	// The signed fix check verifies, names its report, and is not a result.
	env, err := sign.Decode(rep.FixEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sign.Verify(env, pub); err == nil {
		t.Error("signed fix check verified as a result")
	}
	payload, err := sign.VerifyAs(env, pub, sign.FixPayloadType)
	if err != nil {
		t.Fatalf("stored fix envelope does not verify: %v", err)
	}
	var got result.FixCheck
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	id := fixed.String()
	if got.ReportID != id || got.Fix != result.Fixed || got.Report.ReportID != id || got.FixRun.ReportID != id {
		t.Errorf("signed fix check: %+v", got)
	}
	if rep.Envelope == nil {
		t.Error("result envelope missing next to the fix check")
	}
}
