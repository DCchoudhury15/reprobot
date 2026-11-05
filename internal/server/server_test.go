package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/server"
	"github.com/DCchoudhury15/reprobot/internal/store"
	"github.com/DCchoudhury15/reprobot/internal/store/storetest"
)

func TestMain(m *testing.M) { os.Exit(storetest.Main(m)) }

const goodRepro = "version: 1\nproject: tinyparse\ncommit: abc123\nprofile: asan\n"

type env struct {
	st      *store.Store
	ts      *httptest.Server
	dataDir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := storetest.New(t)
	projects := t.TempDir()
	if err := os.WriteFile(filepath.Join(projects, "tinyparse.yaml"), []byte("id: tinyparse\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "other.yaml"), []byte("id: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, dataDir: t.TempDir()}
	srv := &server.Server{Store: st, DataDir: e.dataDir, ProjectsDir: projects}
	e.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(e.ts.Close)
	return e
}

type file struct {
	name string
	data []byte
	// disposition, when set, is sent as the raw Content-Disposition
	// instead of one built from name.
	disposition string
}

func form(t *testing.T, repro string, files []file, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		var w io.Writer
		var err error
		if f.disposition != "" {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", f.disposition)
			w, err = mw.CreatePart(h)
		} else {
			w, err = mw.CreateFormFile("file", f.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		w.Write(f.data)
	}
	if repro != "" {
		w, err := mw.CreateFormFile("repro", "repro.yaml")
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, repro)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func (e *env) post(t *testing.T, body io.Reader, ctype, key string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", e.ts.URL+"/v1/reports", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", ctype)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *env) get(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

type createResp struct {
	ReportID  string `json:"report_id"`
	JobID     int64  `json:"job_id"`
	StatusURL string `json:"status_url"`
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

// entries lists the names in dir; a missing dir is empty.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range des {
		names = append(names, d.Name())
	}
	return names
}

// assertNothingStored checks that no report directory, temp upload or row
// was left behind.
func (e *env) assertNothingStored(t *testing.T) {
	t.Helper()
	if got := entries(t, filepath.Join(e.dataDir, "reports")); len(got) != 0 {
		t.Errorf("report dirs left: %v", got)
	}
	if got := entries(t, filepath.Join(e.dataDir, "tmp")); len(got) != 0 {
		t.Errorf("temp uploads left: %v", got)
	}
	var n int
	if err := e.st.Pool().QueryRow(context.Background(), "SELECT count(*) FROM reports").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d reports stored", n)
	}
}

func TestCreateReport(t *testing.T) {
	e := newEnv(t)
	bin := []byte{0x00, 0xff, 0x10, 'A'}
	body, ctype := form(t, goodRepro,
		[]file{{name: "input.bin", data: bin}, {name: "notes.txt", data: []byte("hello")}},
		map[string]string{"reporter": "alice"})
	code, out := e.post(t, body, ctype, "")
	if code != http.StatusAccepted {
		t.Fatalf("status %d: %s", code, out)
	}
	cr := decode[createResp](t, out)
	if cr.StatusURL != "/v1/reports/"+cr.ReportID || cr.JobID == 0 {
		t.Errorf("response = %+v", cr)
	}

	dir := filepath.Join(e.dataDir, "reports", cr.ReportID)
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", fi.Mode().Perm())
	}
	want := map[string][]byte{"repro.yaml": []byte(goodRepro), "input.bin": bin, "notes.txt": []byte("hello")}
	if got := entries(t, dir); len(got) != len(want) {
		t.Errorf("dir has %v, want %d files", got, len(want))
	}
	for name, data := range want {
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", name, fi.Mode().Perm())
		}
		if got, _ := os.ReadFile(p); !bytes.Equal(got, data) {
			t.Errorf("%s = %q, want %q", name, got, data)
		}
	}
	if got := entries(t, filepath.Join(e.dataDir, "tmp")); len(got) != 0 {
		t.Errorf("temp uploads left: %v", got)
	}

	rep, err := e.st.GetReport(context.Background(), uuid.MustParse(cr.ReportID))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Project != "tinyparse" || rep.Reporter != "alice" || rep.JobState != store.JobQueued ||
		rep.JobID != cr.JobID || rep.ReproHash == "" || !filepath.IsAbs(rep.Dir) {
		t.Errorf("stored report = %+v", rep)
	}
	if same, _ := filepath.Abs(dir); rep.Dir != same {
		t.Errorf("stored dir %q, want %q", rep.Dir, same)
	}
	var jobs int
	e.st.Pool().QueryRow(context.Background(), "SELECT count(*) FROM jobs").Scan(&jobs)
	if jobs != 1 {
		t.Errorf("%d jobs queued, want 1", jobs)
	}

	resp, out := e.get(t, cr.StatusURL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status url: %d %s", resp.StatusCode, out)
	}
	v := decode[map[string]any](t, out)
	if v["state"] != "queued" || v["project"] != "tinyparse" || fmt.Sprint(v["duplicates"]) != "[]" {
		t.Errorf("status = %s", out)
	}
}

func TestCreateReportBadFileName(t *testing.T) {
	var bad []file
	for _, name := range []string{"../evil", "a/b", `a\b`, ".hidden", "..", "repro.yaml", ""} {
		bad = append(bad, file{name: name})
	}
	// RFC 2231 encoded names can carry characters a plain header cannot.
	for _, enc := range []string{"a%0Ab", "..%2Fevil", "a%00b"} {
		bad = append(bad, file{name: enc, disposition: `form-data; name="file"; filename*=UTF-8''` + enc})
	}
	for _, f := range bad {
		t.Run(fmt.Sprintf("%q", f.name), func(t *testing.T) {
			e := newEnv(t)
			// A good file first, so something is on disk when the bad
			// name arrives.
			f.data = []byte("y")
			body, ctype := form(t, goodRepro, []file{{name: "ok.bin", data: []byte("x")}, f}, nil)
			code, out := e.post(t, body, ctype, "")
			if code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", code, out)
			}
			e.assertNothingStored(t)
		})
	}
}

func TestCreateReportBadInput(t *testing.T) {
	cases := map[string]struct {
		repro  string
		files  []file
		fields map[string]string
	}{
		"unknown project":   {repro: "project: nope\n"},
		"bad project id":    {repro: "project: ../tinyparse\n"},
		"no project":        {repro: "commit: abc\n"},
		"not yaml":          {repro: "project: [unclosed\n"},
		"no repro":          {files: []file{{name: "a.bin", data: []byte("x")}}},
		"duplicate file":    {repro: goodRepro, files: []file{{name: "a.bin", data: []byte("x")}, {name: "a.bin", data: []byte("y")}}},
		"unknown field":     {repro: goodRepro, fields: map[string]string{"extra": "x"}},
		"reporter too long": {repro: goodRepro, fields: map[string]string{"reporter": strings.Repeat("a", 300)}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			body, ctype := form(t, c.repro, c.files, c.fields)
			code, out := e.post(t, body, ctype, "")
			if code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", code, out)
			}
			e.assertNothingStored(t)
		})
	}
}

func TestCreateReportBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		w, _ := mw.CreateFormFile("repro", "repro.yaml")
		io.WriteString(w, goodRepro)
		w, _ = mw.CreateFormFile("file", "big.bin")
		chunk := make([]byte, 1<<20)
		var err error
		for i := 0; i < 61 && err == nil; i++ {
			_, err = w.Write(chunk)
		}
		if err == nil {
			err = mw.Close()
		}
		pw.CloseWithError(err)
	}()
	code, out := e.post(t, pr, mw.FormDataContentType(), "")
	pr.Close()
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d: %s", code, out)
	}
	e.assertNothingStored(t)
}

func TestCreateReportIdempotencyKey(t *testing.T) {
	e := newEnv(t)
	body, ctype := form(t, goodRepro, []file{{name: "a.bin", data: []byte("1")}}, nil)
	code, out := e.post(t, body, ctype, "key-1")
	if code != http.StatusAccepted {
		t.Fatalf("first: %d %s", code, out)
	}
	first := decode[createResp](t, out)

	body, ctype = form(t, goodRepro, []file{{name: "a.bin", data: []byte("1")}}, nil)
	code, out = e.post(t, body, ctype, "key-1")
	if code != http.StatusOK {
		t.Fatalf("second: %d %s", code, out)
	}
	if second := decode[createResp](t, out); second != first {
		t.Errorf("second = %+v, want %+v", second, first)
	}

	if got := entries(t, filepath.Join(e.dataDir, "reports")); len(got) != 1 || got[0] != first.ReportID {
		t.Errorf("report dirs = %v, want only %s", got, first.ReportID)
	}
	var reports, jobs int
	e.st.Pool().QueryRow(context.Background(), "SELECT count(*) FROM reports").Scan(&reports)
	e.st.Pool().QueryRow(context.Background(), "SELECT count(*) FROM jobs").Scan(&jobs)
	if reports != 1 || jobs != 1 {
		t.Errorf("reports %d jobs %d, want 1 and 1", reports, jobs)
	}
}

// finish creates a report in project and completes its job with res.
func finish(t *testing.T, st *store.Store, project string, res *result.Result) string {
	t.Helper()
	ctx := context.Background()
	id, _, _, err := st.CreateReport(ctx, store.ReportInput{Project: project, Dir: "/data/reports/x"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.Claim(ctx, "w", time.Minute)
	if err != nil || job == nil || job.ReportID != id {
		t.Fatalf("Claim = %+v, %v", job, err)
	}
	if err := st.Complete(ctx, job.ID, "w", res); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func TestReportPageEscapes(t *testing.T) {
	e := newEnv(t)
	id := finish(t, e.st, "tinyparse", &result.Result{
		State:  result.ReproducedDifferent,
		Reason: "<script>alert(1)</script>",
		Crash: &result.Crash{
			Kind:      `<img src=x onerror="alert(2)">`,
			Frames:    []result.Frame{{Index: 0, Function: "<b>parse_header</b>", File: "a.c", Line: 3}},
			Signature: "sig",
		},
		ClaimFindings: []string{"<script>alert(3)</script>"},
	})
	resp, out := e.get(t, "/reports/"+id)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, out)
	}
	page := string(out)
	if strings.Contains(page, "<script") || strings.Contains(page, "<img") || strings.Contains(page, "<b>") {
		t.Errorf("page contains unescaped markup:\n%s", page)
	}
	for _, want := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;script&gt;alert(3)&lt;/script&gt;",
		"&lt;b&gt;parse_header&lt;/b&gt; a.c:3",
		"REPRODUCED_DIFFERENT",
		"sig",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
}

func TestReportJSONListsDuplicates(t *testing.T) {
	e := newEnv(t)
	crash := func(sig string) *result.Result {
		return &result.Result{State: result.ReproducedAsClaimed, Crash: &result.Crash{Kind: "heap-buffer-overflow", Signature: sig}}
	}
	a := finish(t, e.st, "tinyparse", crash("sig-1"))
	b := finish(t, e.st, "tinyparse", crash("sig-1"))
	c := finish(t, e.st, "other", crash("sig-1"))
	d := finish(t, e.st, "tinyparse", &result.Result{State: result.NotReproduced})

	want := map[string][]string{a: {b}, b: {a}, c: {}, d: {}}
	for id, dups := range want {
		resp, out := e.get(t, "/v1/reports/"+id)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d %s", id, resp.StatusCode, out)
		}
		v := decode[struct {
			State      string         `json:"state"`
			Result     *result.Result `json:"result"`
			Duplicates []string       `json:"duplicates"`
		}](t, out)
		if v.State != store.JobDone || v.Result == nil {
			t.Errorf("%s: state %s result %+v", id, v.State, v.Result)
		}
		if fmt.Sprint(v.Duplicates) != fmt.Sprint(dups) {
			t.Errorf("%s: duplicates %v, want %v", id, v.Duplicates, dups)
		}
	}

	resp, out := e.get(t, "/reports/"+a)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(out), `href="/reports/`+b+`"`) {
		t.Errorf("page of %s does not link duplicate %s:\n%s", a, b, out)
	}
}

func TestReportNotFound(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{
		"/v1/reports/" + uuid.NewString(),
		"/v1/reports/not-a-uuid",
		"/reports/" + uuid.NewString(),
		"/reports/not-a-uuid",
	} {
		if resp, _ := e.get(t, p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	resp, out := e.get(t, "/healthz")
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("healthz = %d %q", resp.StatusCode, out)
	}
}
