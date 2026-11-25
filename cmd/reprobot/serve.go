package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/github"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/server"
	"github.com/DCchoudhury15/reprobot/internal/sign"
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

// serveCmd runs the intake API. With --tokens every route but /healthz
// needs a token; without it the API is open, so it stays on loopback.
func serveCmd(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbURL := fs.String("db", "", "Postgres URL (required)")
	dataDir := fs.String("data", "", "data directory; reports are stored in DIR/reports (required)")
	projectsDir := fs.String("projects", "", "directory with one <project>.yaml per project (required)")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	tokensPath := fs.String("tokens", "", "tokens file (see `reprobot token`); without it the API is open and only a loopback --addr is allowed")
	perHour := fs.Int("reports-per-hour", 20, "most reports one token may send in any hour; 0 means no limit (needs --tokens)")
	signPub := fs.String("sign-pub", "", "public key of the workers' --sign-key, served at /v1/signing-key")
	noAuth := fs.Bool("insecure-no-auth", false, "allow a non-loopback --addr without --tokens, for use behind a proxy that authenticates users")
	ghSecret := fs.String("github-webhook-secret", "", "file with the GitHub App webhook secret; turns on POST /v1/github/webhook")
	ghAppID := fs.String("github-app-id", "", "GitHub App client ID or app ID, to comment on reports that cannot be taken in")
	ghAppKey := fs.String("github-app-key", "", "GitHub App private key (PEM file), with --github-app-id")
	ghAPI := fs.String("github-api-url", github.DefaultBaseURL, "GitHub REST API base URL (for GitHub Enterprise Server)")
	if code := parseFlags(fs, args, "db", "data", "projects"); code >= 0 {
		return code
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "reprobot serve:", err)
		return 1
	}
	if *perHour < 0 {
		return fail(fmt.Errorf("--reports-per-hour must be 0 or more"))
	}
	var pubPEM []byte
	if *signPub != "" {
		if _, err := sign.LoadPublicKey(*signPub); err != nil {
			return fail(err)
		}
		var err error
		if pubPEM, err = os.ReadFile(*signPub); err != nil {
			return fail(err)
		}
	}
	if err := checkAuthConfig(*addr, *tokensPath, *noAuth); err != nil {
		return fail(err)
	}
	var tokens *server.Tokens
	if *tokensPath != "" {
		var err error
		if tokens, err = server.LoadTokens(*tokensPath); err != nil {
			return fail(err)
		}
	}
	if err := checkDir(*projectsDir); err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fail(err)
	}
	app, err := loadGitHubApp(*ghAppID, *ghAppKey, *ghAPI)
	if err != nil {
		return fail(err)
	}
	var intake *server.GitHubIntake
	if *ghSecret != "" {
		secret, err := loadSecret(*ghSecret)
		if err != nil {
			return fail(err)
		}
		intake = &server.GitHubIntake{WebhookSecret: secret, App: app}
	} else if app != nil {
		return fail(fmt.Errorf("--github-app-id needs --github-webhook-secret"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openStore(ctx, *dbURL)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	srv := &server.Server{Store: st, DataDir: *dataDir, ProjectsDir: *projectsDir, Tokens: tokens, ReportsPerHour: *perHour, SigningKeyPEM: pubPEM, GitHub: intake}
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
	if tokens == nil && !isLoopback(*addr) {
		slog.Warn("listening on a non-loopback address without tokens; the API is open to anyone who can reach it", "addr", *addr)
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
	signKey := fs.String("sign-key", "", "sign every result with this key (see reprobot keygen)")
	ghAppID := fs.String("github-app-id", "", "GitHub App client ID or app ID; posts verdicts on reports that came from GitHub")
	ghAppKey := fs.String("github-app-key", "", "GitHub App private key (PEM file), with --github-app-id")
	ghAPI := fs.String("github-api-url", github.DefaultBaseURL, "GitHub REST API base URL (for GitHub Enterprise Server)")
	publicURL := fs.String("public-url", "", "base URL of serve, for result links in GitHub comments (for example https://reprobot.example.org)")
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
	app, err := loadGitHubApp(*ghAppID, *ghAppKey, *ghAPI)
	if err != nil {
		return fail(err)
	}
	if *publicURL != "" && !strings.HasPrefix(*publicURL, "https://") && !strings.HasPrefix(*publicURL, "http://") {
		return fail(fmt.Errorf("--public-url must start with https:// or http://"))
	}
	var key ed25519.PrivateKey
	if *signKey != "" {
		var err error
		if key, err = sign.LoadPrivateKey(*signKey); err != nil {
			return fail(err)
		}
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
		SignKey:      key,
	}
	slog.Info("worker started", "concurrency", *concurrency, "lease", *lease, "github", app != nil)
	var notified sync.WaitGroup
	if app != nil {
		n := &worker.Notifier{Store: st, GitHub: app, PublicURL: *publicURL}
		notified.Go(func() { n.Serve(ctx) })
	}
	err = w.Serve(ctx)
	notified.Wait()
	if err != nil {
		return fail(err)
	}
	slog.Info("worker stopped")
	return 0
}

// loadGitHubApp returns nil when neither flag is set.
func loadGitHubApp(id, keyPath, apiURL string) (*github.App, error) {
	if id == "" && keyPath == "" {
		return nil, nil
	}
	if id == "" || keyPath == "" {
		return nil, fmt.Errorf("--github-app-id and --github-app-key go together")
	}
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	key, err := github.ParseKey(b)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(apiURL, "https://") && !strings.HasPrefix(apiURL, "http://") {
		return nil, fmt.Errorf("--github-api-url must start with https:// or http://")
	}
	return &github.App{ID: id, Key: key, BaseURL: apiURL}, nil
}

// loadSecret reads a secret file, without the trailing line break.
func loadSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = []byte(strings.TrimRight(string(b), "\r\n"))
	if len(b) < 16 {
		return nil, fmt.Errorf("%s: the webhook secret should be at least 16 characters", path)
	}
	return b, nil
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

// checkAuthConfig refuses to serve an open API on a non-loopback address
// unless the operator says a proxy in front does the authentication.
func checkAuthConfig(addr, tokensPath string, noAuth bool) error {
	switch {
	case tokensPath != "" && noAuth:
		return errors.New("--tokens and --insecure-no-auth cannot be used together")
	case tokensPath == "" && !noAuth && !isLoopback(addr):
		return fmt.Errorf("--addr %s is not a loopback address: give --tokens, or --insecure-no-auth if a proxy authenticates users", addr)
	}
	return nil
}

// tokenCmd prints a new API token. The tokens file line goes to stdout, so
// `reprobot token NAME >> tokens` adds it; the token itself goes to stderr
// and is shown only this once.
func tokenCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: reprobot token NAME >> tokens-file")
		return 2
	}
	token, line, err := server.NewToken(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "reprobot token:", err)
		return 2
	}
	fmt.Fprintf(stderr, "token for %s (shown once, keep it secret):\n%s\n", args[0], token)
	fmt.Fprintln(stdout, line)
	return 0
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
