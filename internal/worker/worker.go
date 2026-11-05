// Package worker takes jobs from the store queue and runs them.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/store"
)

// RunFunc checks one reproducer. runner.Run has this type.
type RunFunc func(ctx context.Context, o runner.Options) (*result.Result, error)

// Defaults for zero fields of Worker.
const (
	DefaultLease        = 5 * time.Minute
	DefaultPollInterval = time.Second
)

// storeTimeout bounds Complete and Fail calls, which still run after the
// worker context is cancelled.
const storeTimeout = 30 * time.Second

// Worker runs Concurrency loops. Each loop claims a job, runs it with Run,
// keeps the lease alive with a heartbeat every Lease/3, and stores the
// result (Complete) or the error (Fail). If the lease is lost, the run's
// context is cancelled and nothing is written.
type Worker struct {
	Store        *store.Store
	Run          RunFunc
	ProjectsDir  string // holds <project>.yaml files
	CacheDir     string
	Concurrency  int           // default 1
	Lease        time.Duration // default DefaultLease
	PollInterval time.Duration // wait when the queue is empty; default DefaultPollInterval
	ID           string        // lease owner prefix; default hostname-pid
}

// Serve runs the loops until ctx is cancelled and all of them have
// stopped. A job that is running when ctx is cancelled is cancelled too and
// recorded with Fail, so it is retried later.
func (w *Worker) Serve(ctx context.Context) error {
	if w.Store == nil || w.Run == nil {
		return errors.New("worker: Store and Run must be set")
	}
	if w.ProjectsDir == "" {
		return errors.New("worker: ProjectsDir must be set")
	}
	n := max(w.Concurrency, 1)
	id := w.ID
	if id == "" {
		host, _ := os.Hostname()
		id = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	var wg sync.WaitGroup
	for i := range n {
		// Each loop has its own owner name, so leases of two loops in one
		// process never look the same.
		owner := fmt.Sprintf("%s/%d", id, i)
		wg.Go(func() { w.loop(ctx, owner) })
	}
	wg.Wait()
	return nil
}

func (w *Worker) lease() time.Duration {
	if w.Lease > 0 {
		return w.Lease
	}
	return DefaultLease
}

func (w *Worker) pollInterval() time.Duration {
	if w.PollInterval > 0 {
		return w.PollInterval
	}
	return DefaultPollInterval
}

func (w *Worker) loop(ctx context.Context, owner string) {
	for ctx.Err() == nil {
		job, err := w.Store.Claim(ctx, owner, w.lease())
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("claim job", "worker", owner, "err", err)
			}
			sleep(ctx, w.pollInterval())
			continue
		}
		if job == nil {
			sleep(ctx, w.pollInterval())
			continue
		}
		w.process(ctx, owner, job)
	}
}

func (w *Worker) process(ctx context.Context, owner string, job *store.Job) {
	log := slog.With("worker", owner, "job", job.ID, "report", job.ReportID, "attempt", job.Attempts)
	log.Info("job started")

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		w.heartbeat(runCtx, cancel, owner, job.ID)
	}()

	res, err := w.Run(runCtx, runner.Options{
		ProjectPath: filepath.Join(w.ProjectsDir, job.Project+".yaml"),
		ReproPath:   filepath.Join(job.Dir, "repro.yaml"),
		CacheDir:    w.CacheDir,
	})
	leaseLost := errors.Is(context.Cause(runCtx), store.ErrLeaseLost)
	cancel(nil)
	<-hbDone
	if leaseLost {
		log.Warn("lease lost, result dropped")
		return
	}

	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer scancel()
	switch {
	case err != nil:
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "worker stopped during the run: " + msg
		}
		log.Warn("job failed", "err", msg)
		err = w.Store.Fail(sctx, job.ID, owner, msg)
	case res == nil:
		log.Warn("job failed", "err", "runner returned no result")
		err = w.Store.Fail(sctx, job.ID, owner, "runner returned no result")
	default:
		log.Info("job done", "state", res.State)
		err = w.Store.Complete(sctx, job.ID, owner, res)
	}
	if err != nil {
		log.Error("store job outcome", "err", err)
	}
}

// heartbeat extends the lease every Lease/3 until ctx ends. When the lease
// is lost it cancels the run with ErrLeaseLost as the cause.
func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, owner string, jobID int64) {
	t := time.NewTicker(w.lease() / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := w.Store.Heartbeat(ctx, jobID, owner, w.lease())
		if errors.Is(err, store.ErrLeaseLost) {
			cancel(store.ErrLeaseLost)
			return
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("heartbeat", "worker", owner, "job", jobID, "err", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
