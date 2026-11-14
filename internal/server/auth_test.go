package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/server"
)

// tokensFile writes the lines for new tokens named names and returns the
// loaded tokens and the token of each name.
func tokensFile(t *testing.T, names ...string) (*server.Tokens, map[string]string) {
	t.Helper()
	secret := map[string]string{}
	var lines []string
	for _, n := range names {
		tok, line, err := server.NewToken(n)
		if err != nil {
			t.Fatal(err)
		}
		secret[n] = tok
		lines = append(lines, line)
	}
	path := filepath.Join(t.TempDir(), "tokens")
	content := "# reprobot tokens\n\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tokens, err := server.LoadTokens(path)
	if err != nil {
		t.Fatal(err)
	}
	return tokens, secret
}

func TestTokensGuardEveryRouteButHealth(t *testing.T) {
	tokens, secret := tokensFile(t, "alice", "maint@example.org")
	e := newEnvTokens(t, tokens)
	id := finish(t, e.st, "tinyparse", &result.Result{State: result.NotReproduced})

	do := func(method, path string, auth func(*http.Request)) (int, http.Header, string) {
		t.Helper()
		var body io.Reader
		ctype := ""
		if method == "POST" {
			b, c := form(t, goodRepro, nil, nil)
			body, ctype = b, c
		}
		req, err := http.NewRequest(method, e.ts.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if auth != nil {
			auth(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(out)
	}
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	basic := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.SetBasicAuth("anyone", tok) }
	}
	other, _, _ := server.NewToken("mallory")
	bad := map[string]func(*http.Request){
		"no header":         nil,
		"unknown token":     bearer(other),
		"empty bearer":      bearer(""),
		"token as user":     func(r *http.Request) { r.SetBasicAuth(secret["alice"], "") },
		"token with suffix": bearer(secret["alice"] + "x"),
		"truncated token":   bearer(secret["alice"][:20]),
		"other scheme":      func(r *http.Request) { r.Header.Set("Authorization", "Token "+secret["alice"]) },
		"very long token":   bearer(strings.Repeat("a", 5000)),
	}
	routes := [][2]string{{"POST", "/v1/reports"}, {"GET", "/v1/reports/" + id}, {"GET", "/reports/" + id}}
	for _, rt := range routes {
		for name, auth := range bad {
			code, h, out := do(rt[0], rt[1], auth)
			if code != http.StatusUnauthorized || !strings.Contains(strings.Join(h.Values("WWW-Authenticate"), ","), "Basic") {
				t.Errorf("%s %s with %s: %d %v %s", rt[0], rt[1], name, code, h.Values("WWW-Authenticate"), out)
			}
		}
	}
	e.assertNoNewReports(t, 1)

	okCodes := map[string]int{"POST": http.StatusAccepted, "GET": http.StatusOK}
	for _, rt := range routes {
		for _, auth := range []func(*http.Request){bearer(secret["alice"]), basic(secret["maint@example.org"]),
			func(r *http.Request) { r.Header.Set("Authorization", "bearer  "+secret["alice"]) }} {
			if code, _, out := do(rt[0], rt[1], auth); code != okCodes[rt[0]] {
				t.Errorf("%s %s with a good token: %d %s", rt[0], rt[1], code, out)
			}
		}
	}
	if code, _, out := do("GET", "/healthz", nil); code != http.StatusOK {
		t.Errorf("healthz without a token: %d %s", code, out)
	}
}

// assertNoNewReports checks that only want reports exist.
func (e *env) assertNoNewReports(t *testing.T, want int) {
	t.Helper()
	var n int
	if err := e.st.Pool().QueryRow(t.Context(), "SELECT count(*) FROM reports").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Errorf("%d reports stored, want %d", n, want)
	}
	if got := entries(t, filepath.Join(e.dataDir, "tmp")); len(got) != 0 {
		t.Errorf("temp uploads left: %v", got)
	}
}

func TestNewToken(t *testing.T) {
	tok, line, err := server.NewToken("alice")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(tok))
	if line != "alice "+hex.EncodeToString(sum[:]) || len(tok) != 43 {
		t.Errorf("token %q line %q", tok, line)
	}
	if tok2, _, _ := server.NewToken("alice"); tok2 == tok {
		t.Error("two tokens are the same")
	}
	for _, name := range []string{"", "-x", "a b", "a\nb", strings.Repeat("a", 65)} {
		if _, _, err := server.NewToken(name); err == nil {
			t.Errorf("NewToken(%q) = nil error", name)
		}
	}
}

func TestLoadTokensRejects(t *testing.T) {
	_, line, _ := server.NewToken("alice")
	_, line2, _ := server.NewToken("bob")
	hash := strings.Fields(line)[1]
	for name, content := range map[string]string{
		"empty":           "",
		"only comments":   "# nothing\n\n",
		"one field":       "alice\n",
		"three fields":    line + " extra\n",
		"short hash":      "alice " + hash[:60] + "\n",
		"not hex":         "alice " + strings.Repeat("z", 64) + "\n",
		"bad name":        "-alice " + hash + "\n",
		"duplicate name":  line + "\n" + "alice " + strings.Fields(line2)[1] + "\n",
		"duplicate token": line + "\n" + "bob " + hash + "\n",
	} {
		path := filepath.Join(t.TempDir(), "tokens")
		os.WriteFile(path, []byte(content), 0o644)
		if _, err := server.LoadTokens(path); err == nil {
			t.Errorf("%s: LoadTokens = nil error", name)
		}
	}
	if _, err := server.LoadTokens(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing file: nil error")
	}
}
