package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/github"
	"github.com/DCchoudhury15/reprobot/internal/server"
	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

var hookSecret = []byte("0123456789abcdef-webhook")

// projectYAML is a valid project whose repo is a GitHub repository.
func projectYAML(t *testing.T, id, repo string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.FixtureDir(), "project.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(b), "{{REPO}}", repo, 1)
	return []byte(strings.Replace(s, "id: tinyparse", "id: "+id, 1))
}

// fakeComments stands in for the GitHub API: the token endpoint and the
// advisory comments.
type fakeComments struct {
	mu   sync.Mutex
	got  []string // "path\nbody"
	fail bool
}

func (f *fakeComments) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/access_tokens") {
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"token":"ghs_x","expires_at":"2099-01-01T00:00:00Z"}`)
		return
	}
	var in struct{ Body string }
	json.NewDecoder(r.Body).Decode(&in)
	f.got = append(f.got, r.URL.Path+"\n"+in.Body)
	if f.fail {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (f *fakeComments) comments() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

type ghEnv struct {
	*env
	api      *fakeComments
	projects string
}

func newGitHubEnv(t *testing.T, set func(*server.Server)) *ghEnv {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeComments{}
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)
	g := &ghEnv{api: api}
	g.env = newEnvWith(t, func(s *server.Server) {
		g.projects = s.ProjectsDir
		s.GitHub = &server.GitHubIntake{WebhookSecret: hookSecret, App: &github.App{ID: "1", Key: key, BaseURL: apiSrv.URL}}
		if set != nil {
			set(s)
		}
	})
	if err := os.WriteFile(filepath.Join(g.projects, "tinyparse.yaml"), projectYAML(t, "tinyparse", "https://github.com/octo-org/tinyparse.git"), 0o644); err != nil {
		t.Fatal(err)
	}
	return g
}

// payload is the recorded delivery with the reprobot block filled in.
func payload(t *testing.T, change func(desc string) string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "github", "testdata", "advisory_reported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	adv := m["repository_advisory"].(map[string]any)
	desc := adv["description"].(string)
	desc = strings.Replace(desc, "COMMIT", "abc123", 1)
	desc = strings.Replace(desc, "INPUT", base64.StdEncoding.EncodeToString(inputBin), 1)
	if change != nil {
		desc = change(desc)
	}
	adv["description"] = desc
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var inputBin = append([]byte{200}, bytes.Repeat([]byte("A"), 200)...)

type hookAnswer struct {
	Status   string `json:"status"`
	Reason   string `json:"reason"`
	ReportID string `json:"report_id"`
	Error    string `json:"error"`
}

func (g *ghEnv) deliver(t *testing.T, event string, body []byte, sig string) (int, hookAnswer) {
	t.Helper()
	req, err := http.NewRequest("POST", g.ts.URL+"/v1/github/webhook", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", "72d3162e-cc78-11e3-81ab-4c9367dc0958")
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var a hookAnswer
	b, _ := io.ReadAll(resp.Body)
	json.Unmarshal(b, &a)
	return resp.StatusCode, a
}

func (g *ghEnv) send(t *testing.T, body []byte) (int, hookAnswer) {
	t.Helper()
	return g.deliver(t, "repository_advisory", body, github.Sign(hookSecret, body))
}

func (g *ghEnv) reports(t *testing.T) int {
	t.Helper()
	var n int
	if err := g.st.Pool().QueryRow(context.Background(), "SELECT count(*) FROM reports").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGitHubWebhookQueuesReport(t *testing.T) {
	g := newGitHubEnv(t, nil)
	body := payload(t, nil)
	code, a := g.send(t, body)
	if code != http.StatusAccepted || a.Status != "queued" || a.ReportID == "" {
		t.Fatalf("got %d %+v", code, a)
	}
	var (
		dir, reporter, repo, ghsa, key string
		inst                           int64
		owner                          *string
	)
	err := g.st.Pool().QueryRow(context.Background(), `
		SELECT dir, reporter, owner, idempotency_key, github_installation, github_repo, github_ghsa
		FROM reports WHERE id = $1`, a.ReportID).Scan(&dir, &reporter, &owner, &key, &inst, &repo, &ghsa)
	if err != nil {
		t.Fatal(err)
	}
	if reporter != "github:mona" || owner != nil || key != "github:GHSA-8f4m-hccc-8qph" ||
		inst != 52219614 || repo != "octo-org/tinyparse" || ghsa != "GHSA-8f4m-hccc-8qph" {
		t.Errorf("row: reporter %q owner %v key %q github %d %q %q", reporter, owner, key, inst, repo, ghsa)
	}
	repro, err := os.ReadFile(filepath.Join(dir, "repro.yaml"))
	if err != nil || !strings.Contains(string(repro), "project: tinyparse\ncommit: abc123\n") {
		t.Errorf("repro.yaml = %q, %v", repro, err)
	}
	if in, err := os.ReadFile(filepath.Join(dir, "input.bin")); err != nil || !bytes.Equal(in, inputBin) {
		t.Errorf("input.bin = %q, %v", in, err)
	}

	// GitHub redelivers on timeouts: the same advisory is the same report.
	code, again := g.send(t, body)
	if code != http.StatusOK || again.Status != "duplicate" || again.ReportID != a.ReportID || g.reports(t) != 1 {
		t.Errorf("redelivery: %d %+v, %d reports", code, again, g.reports(t))
	}
	if c := g.api.comments(); len(c) != 0 {
		t.Errorf("comments on a good report: %q", c)
	}
}

func TestGitHubWebhookSignature(t *testing.T) {
	g := newGitHubEnv(t, nil)
	body := payload(t, nil)
	for name, sig := range map[string]string{
		"none":         "",
		"other secret": github.Sign([]byte("not the secret at all"), body),
		"other body":   github.Sign(hookSecret, append([]byte(" "), body...)),
	} {
		if code, a := g.deliver(t, "repository_advisory", body, sig); code != http.StatusUnauthorized {
			t.Errorf("%s: %d %+v", name, code, a)
		}
	}
	if g.reports(t) != 0 {
		t.Error("an unsigned delivery made a report")
	}
}

func TestGitHubWebhookIgnores(t *testing.T) {
	g := newGitHubEnv(t, nil)
	ping := []byte(`{"zen":"Design for failure.","hook_id":1}`)
	if code, a := g.deliver(t, "ping", ping, github.Sign(hookSecret, ping)); code != http.StatusOK || a.Status != "pong" {
		t.Errorf("ping: %d %+v", code, a)
	}
	issue := []byte(`{"action":"opened"}`)
	if code, a := g.deliver(t, "issues", issue, github.Sign(hookSecret, issue)); code != http.StatusAccepted || a.Status != "ignored" {
		t.Errorf("issues: %d %+v", code, a)
	}
	for name, body := range map[string][]byte{
		"published":     bytes.Replace(payload(t, nil), []byte(`"action":"reported"`), []byte(`"action":"published"`), 1),
		"other repo":    bytes.Replace(payload(t, nil), []byte(`"full_name":"octo-org/tinyparse"`), []byte(`"full_name":"octo-org/other"`), 1),
		"no block":      payload(t, func(string) string { return "Plain text report, no reprobot block." }),
		"only fenced c": payload(t, func(string) string { return "```c\nint x;\n```" }),
	} {
		if bytes.Equal(body, payload(t, nil)) {
			t.Fatalf("%s: payload unchanged", name)
		}
		code, a := g.send(t, body)
		if code != http.StatusAccepted || a.Status != "ignored" {
			t.Errorf("%s: %d %+v", name, code, a)
		}
	}
	if g.reports(t) != 0 || len(g.api.comments()) != 0 {
		t.Errorf("%d reports, comments %q", g.reports(t), g.api.comments())
	}
}

// A reprobot block that cannot be used gets a comment saying why, so the
// reporter can fix it. Nothing is queued.
func TestGitHubWebhookRejects(t *testing.T) {
	g := newGitHubEnv(t, nil)
	if err := os.WriteFile(filepath.Join(g.projects, "other.yaml"), projectYAML(t, "other", "https://github.com/octo-org/other"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		change func(string) string
		want   string
	}{
		"bad base64": {func(d string) string {
			return strings.Replace(d, "```reprobot-file input.bin\n", "```reprobot-file input.bin\n!!", 1)
		}, "not valid base64"},
		"other project": {func(d string) string { return strings.Replace(d, "project: tinyparse", "project: other", 1) }, `repro.yaml names project "other", but this repository is project "tinyparse"`},
		"path in name": {func(d string) string {
			return strings.Replace(d, "reprobot-file input.bin", "reprobot-file ../input.bin", 1)
		}, "path separator"},
		"dot name": {func(d string) string {
			return strings.Replace(d, "reprobot-file input.bin", "reprobot-file .input.bin", 1)
		}, "starts with a dot"},
		"repro name": {func(d string) string {
			return strings.Replace(d, "reprobot-file input.bin", "reprobot-file repro.yaml", 1)
		}, "reserved"},
		"unknown": {func(d string) string { return strings.Replace(d, "project: tinyparse", "project: nope", 1) }, `unknown project "nope"`},
		"twice": {func(d string) string {
			return d + "\n```reprobot-file input.bin\nAA==\n```\n"
		}, "sent twice"},
	}
	n := 0
	for name, c := range cases {
		code, a := g.send(t, payload(t, c.change))
		if code != http.StatusAccepted || a.Status != "rejected" || !strings.Contains(a.Reason, c.want) {
			t.Errorf("%s: %d %+v, want reason %q", name, code, a, c.want)
			continue
		}
		n++
		got := g.api.comments()
		if len(got) != n {
			t.Fatalf("%s: %d comments, want %d", name, len(got), n)
		}
		path, body, _ := strings.Cut(got[n-1], "\n")
		if path != "/repos/octo-org/tinyparse/security-advisories/GHSA-8f4m-hccc-8qph/comments" ||
			!strings.Contains(body, "reprobot could not take this report in") || !strings.Contains(body, c.want[:min(len(c.want), 10)]) {
			t.Errorf("%s: comment %q", name, got[n-1])
		}
	}
	if g.reports(t) != 0 {
		t.Errorf("%d reports were queued", g.reports(t))
	}
	entries, _ := os.ReadDir(filepath.Join(g.dataDir, "tmp"))
	if len(entries) != 0 {
		t.Errorf("upload dirs left behind: %d", len(entries))
	}
}

// Two projects for one repository is a set-up mistake; nothing is queued.
func TestGitHubWebhookAmbiguousRepo(t *testing.T) {
	g := newGitHubEnv(t, nil)
	if err := os.WriteFile(filepath.Join(g.projects, "other.yaml"), projectYAML(t, "other", "git@github.com:Octo-Org/TinyParse.git"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, a := g.send(t, payload(t, nil))
	if code != http.StatusAccepted || a.Status != "ignored" || !strings.Contains(a.Reason, "more than one project is set up for octo-org/tinyparse: other, tinyparse") {
		t.Errorf("got %d %+v", code, a)
	}
}

// The webhook needs no API token, and is off without a secret.
func TestGitHubWebhookAuth(t *testing.T) {
	tokens, _ := tokensFile(t, "alice")
	g := newGitHubEnv(t, func(s *server.Server) { s.Tokens = tokens })
	if code, a := g.send(t, payload(t, nil)); code != http.StatusAccepted || a.Status != "queued" {
		t.Errorf("with tokens: %d %+v", code, a)
	}
	off := newEnv(t)
	body := payload(t, nil)
	req, _ := http.NewRequest("POST", off.ts.URL+"/v1/github/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "repository_advisory")
	req.Header.Set("X-Hub-Signature-256", github.Sign(hookSecret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("without GitHub intake: %d", resp.StatusCode)
	}
}

// An upload may not take the key a GitHub report will use.
func TestGitHubKeyIsReserved(t *testing.T) {
	e := newEnv(t)
	body, ctype := form(t, goodRepro, nil, nil)
	code, out := e.post(t, body, ctype, "github:GHSA-8f4m-hccc-8qph")
	if code != http.StatusBadRequest || !strings.Contains(string(out), "may not start with github:") {
		t.Errorf("got %d %s", code, out)
	}
}
