package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/server"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/worker"
)

const shutdownTimeout = 30 * time.Second

// parseFlags parses args and checks that the named flags are set. It
// returns -1 on success, or the exit code to use.
func parseFlags(fs *flag.FlagSet, args []string, required ...string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "reprobot %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		fs.Usage()
		return 2
	}
	for _, name := range required {
		if fs.Lookup(name).Value.String() == "" {
			fmt.Fprintf(os.Stderr, "reprobot %s: --%s is required\n", fs.Name(), name)
			fs.Usage()
			return 2
		}
	}
	return -1
}

// openStore connects and applies migrations.
func openStore(ctx context.Context, url string) (*store.Store, error) {
	st, err := store.Open(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return st, nil
}

// serveCmd runs the intake API. It has no authentication, so it listens on
// 127.0.0.1 unless told otherwise.
func serveCmd(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbURL := fs.String("db", "", "Postgres URL (required)")
	dataDir := fs.String("data", "", "data directory; reports are stored in DIR/reports (required)")
	projectsDir := fs.String("projects", "", "directory with one <project>.yaml per project (required)")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address; there is no authentication, so keep it on loopback")
	if code := parseFlags(fs, args, "db", "data", "projects"); code >= 0 {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "reprobot serve:", err)
		return 1
	}
	if err := checkDir(*projectsDir); err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx, *dbURL)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	srv := &server.Server{Store: st, DataDir: *dataDir, ProjectsDir: *projectsDir}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // uploads can be up to 60 MB
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fail(err)
	}
	if !isLoopback(*addr) {
		slog.Warn("listening on a non-loopback address; the API has no authentication", "addr", *addr)
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	slog.Info("serving", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return fail(err)
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(sctx); err != nil {
		return fail(err)
	}
	return 0
}

// workerCmd runs queued jobs with runner.Run until SIGINT or SIGTERM.
func workerCmd(args []string) int {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	dbURL := fs.String("db", "", "Postgres URL (required)")
	dataDir := fs.String("data", "", "data directory shared with serve (required)")
	projectsDir := fs.String("projects", "", "directory with one <project>.yaml per project (required)")
	cacheDir := fs.String("cache", "", "git mirrors and build cache (default DATA/cache)")
	concurrency := fs.Int("concurrency", 2, "jobs to run at the same time")
	lease := fs.Duration("lease", worker.DefaultLease, "job lease; renewed every lease/3 while a job runs")
	poll := fs.Duration("poll", worker.DefaultPollInterval, "wait between checks when the queue is empty")
	if code := parseFlags(fs, args, "db", "data", "projects"); code >= 0 {
		return code
	}
	if *concurrency < 1 || *lease <= 0 || *poll <= 0 {
		fmt.Fprintln(os.Stderr, "reprobot worker: --concurrency, --lease and --poll must be positive")
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "reprobot worker:", err)
		return 1
	}
	if err := checkDir(*projectsDir); err != nil {
		return fail(err)
	}
	if *cacheDir == "" {
		*cacheDir = filepath.Join(*dataDir, "cache")
	}
	projects, err := filepath.Abs(*projectsDir)
	if err != nil {
		return fail(err)
	}
	cache, err := filepath.Abs(*cacheDir)
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx, *dbURL)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	w := &worker.Worker{
		Store:        st,
		Run:          runner.Run,
		ProjectsDir:  projects,
		CacheDir:     cache,
		Concurrency:  *concurrency,
		Lease:        *lease,
		PollInterval: *poll,
	}
	slog.Info("worker started", "concurrency", *concurrency, "lease", *lease)
	if err := w.Serve(ctx); err != nil {
		return fail(err)
	}
	slog.Info("worker stopped")
	return 0
}

func checkDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
