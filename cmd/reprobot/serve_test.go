package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/server"
)

func TestCheckAuthConfig(t *testing.T) {
	for _, c := range []struct {
		addr, tokens string
		noAuth, ok   bool
	}{
		{"127.0.0.1:8080", "", false, true},
		{"[::1]:8080", "", false, true},
		{"localhost:8080", "", false, true},
		{"0.0.0.0:8080", "", false, false},
		{":8080", "", false, false},
		{"10.0.0.5:8080", "", false, false},
		{"0.0.0.0:8080", "/etc/tokens", false, true},
		{"0.0.0.0:8080", "", true, true},
		{"127.0.0.1:8080", "/etc/tokens", true, false},
	} {
		err := checkAuthConfig(c.addr, c.tokens, c.noAuth)
		if (err == nil) != c.ok {
			t.Errorf("checkAuthConfig(%q, %q, %v) = %v, want ok %v", c.addr, c.tokens, c.noAuth, err, c.ok)
		}
	}
}

func TestTokenCmd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := tokenCmd([]string{"alice"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	// The token is on the last stderr line, the file line on stdout.
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	token := lines[len(lines)-1]
	sum := sha256.Sum256([]byte(token))
	if want := "alice " + hex.EncodeToString(sum[:]) + "\n"; stdout.String() != want {
		t.Errorf("stdout %q, want %q", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), token) {
		t.Error("the token itself went to stdout, so `>> tokens` would store it")
	}
	path := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(path, stdout.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := server.LoadTokens(path); err != nil {
		t.Errorf("LoadTokens of the printed line: %v", err)
	}

	for _, args := range [][]string{nil, {"a", "b"}, {"-h"}, {"bad name"}} {
		stdout.Reset()
		if code := tokenCmd(args, &stdout, &bytes.Buffer{}); code != 2 || stdout.Len() != 0 {
			t.Errorf("tokenCmd(%q) = %d, stdout %q", args, code, stdout.String())
		}
	}
}
