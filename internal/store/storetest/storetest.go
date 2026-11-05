// Package storetest gives tests a fresh, migrated Postgres database.
//
// By default it starts one postgres:18-alpine container per test binary,
// on a random port bound to 127.0.0.1, and removes it when the tests end.
// Set REPROBOT_TEST_DATABASE_URL to use an existing server instead; the user
// in that URL must be allowed to create databases. Tests are skipped when
// neither is available.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/DCchoudhury15/reprobot/internal/store"
)

// EnvURL names the variable that points tests at an existing Postgres.
const EnvURL = "REPROBOT_TEST_DATABASE_URL"

const image = "postgres:18-alpine"

var (
	once        sync.Once
	adminURL    string
	skipReason  string
	startErr    error
	mu          sync.Mutex
	containerID string
)

// Main runs the tests and removes the Postgres container, if one was
// started. Use it from TestMain: os.Exit(storetest.Main(m)).
func Main(m *testing.M) int {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		stop()
		os.Exit(1)
	}()
	code := m.Run()
	signal.Stop(sig)
	stop()
	return code
}

// New returns a Store on a new, migrated database that is dropped when the
// test ends. It skips the test when no Postgres is available.
func New(t testing.TB) *store.Store {
	t.Helper()
	dbURL := NewDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// NewDatabase creates an empty database (no migrations) that is dropped
// when the test ends, and returns its connection URL. It skips the test
// when no Postgres is available.
func NewDatabase(t testing.TB) string {
	t.Helper()
	once.Do(setup)
	if skipReason != "" {
		t.Skip(skipReason)
	}
	if startErr != nil {
		t.Fatalf("start postgres: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "reprobot_test_" + randomHex(8)
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	// Cleanups run last in, first out, so this runs after the caller has
	// closed its connections.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Errorf("drop database: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database: %v", err)
		}
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func setup() {
	if u := os.Getenv(EnvURL); u != "" {
		adminURL = u
		return
	}
	if err := exec.Command("docker", "version").Run(); err != nil {
		skipReason = fmt.Sprintf("docker is not usable (%v) and %s is not set", err, EnvURL)
		return
	}
	// A fresh random password for each throwaway container.
	pwBytes := make([]byte, 16)
	if _, err := rand.Read(pwBytes); err != nil {
		startErr = err
		return
	}
	pw := hex.EncodeToString(pwBytes)
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"--label", "reprobot-test=1",
		"-e", "POSTGRES_PASSWORD="+pw,
		"-p", "127.0.0.1::5432",
		image,
		"-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off",
	).Output()
	if err != nil {
		startErr = fmt.Errorf("docker run: %w%s", err, stderrOf(err))
		return
	}
	id := strings.TrimSpace(string(out))
	mu.Lock()
	containerID = id
	mu.Unlock()

	out, err = exec.Command("docker", "port", id, "5432/tcp").Output()
	if err != nil {
		startErr = fmt.Errorf("docker port: %w%s", err, stderrOf(err))
		return
	}
	addr := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	u := url.URL{Scheme: "postgres", User: url.UserPassword("postgres", pw), Host: addr, Path: "/postgres", RawQuery: "sslmode=disable"}
	adminURL = u.String()

	// The image's first-start init runs a server that only listens on a
	// unix socket, so a TCP connection means the real server is up.
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, err := pgx.Connect(ctx, adminURL)
		if err == nil {
			err = conn.Ping(ctx)
			conn.Close(ctx)
		}
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			startErr = fmt.Errorf("postgres did not accept connections: %w", err)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func stop() {
	mu.Lock()
	id := containerID
	containerID = ""
	mu.Unlock()
	if id != "" {
		_ = exec.Command("docker", "rm", "-f", id).Run()
	}
}

func stderrOf(err error) string {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return ": " + strings.TrimSpace(string(ee.Stderr))
	}
	return ""
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
