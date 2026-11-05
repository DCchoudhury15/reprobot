package worker_test

import (
	"context"
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
