package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGitHubApp(t *testing.T) {
	dir := t.TempDir()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "app.pem")
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0o600)
	bad := filepath.Join(dir, "bad.pem")
	os.WriteFile(bad, []byte("not a key"), 0o600)

	if app, err := loadGitHubApp("", "", ""); app != nil || err != nil {
		t.Errorf("no flags: %v %v", app, err)
	}
	if app, err := loadGitHubApp("Iv23li", keyPath, "https://ghe.example.org/api/v3"); err != nil || app.ID != "Iv23li" || !app.Key.Equal(k) || app.BaseURL != "https://ghe.example.org/api/v3" {
		t.Errorf("both flags: %+v %v", app, err)
	}
	if _, err := loadGitHubApp("Iv23li", keyPath, "ftp://x"); err == nil {
		t.Error("ftp API URL accepted")
	}
	for name, args := range map[string][2]string{
		"id only":  {"Iv23li", ""},
		"key only": {"", keyPath},
		"bad key":  {"Iv23li", bad},
		"no file":  {"Iv23li", filepath.Join(dir, "missing")},
	} {
		if _, err := loadGitHubApp(args[0], args[1], "https://api.github.com"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLoadSecret(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte("0123456789abcdef-secret\r\n"), 0o600)
	if b, err := loadSecret(good); err != nil || string(b) != "0123456789abcdef-secret" {
		t.Errorf("got %q %v", b, err)
	}
	short := filepath.Join(dir, "short")
	os.WriteFile(short, []byte("abc\n"), 0o600)
	if _, err := loadSecret(short); err == nil {
		t.Error("short secret accepted")
	}
}

// The webhook comment flags make no sense without the webhook.
func TestServeGitHubFlags(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPath := filepath.Join(t.TempDir(), "app.pem")
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0o600)
	// Fails before the database is opened, so the URL is never used.
	stderr := captureStderr(t, func() {
		code := serveCmd([]string{"--db", "postgres://x", "--data", t.TempDir(), "--projects", t.TempDir(),
			"--github-app-id", "1", "--github-app-key", keyPath})
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
	})
	if !strings.Contains(stderr, "--github-app-id needs --github-webhook-secret") {
		t.Errorf("stderr = %q", stderr)
	}
}

func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	os.Stderr = old
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}
