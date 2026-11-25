// Package server is the HTTP intake API and the result pages.
//
// With Tokens set, every route except /healthz needs an API token, and a
// token reads only its own reports and those of the projects it maintains.
// Without them anyone who can reach the address can submit reports and
// read every result and reporter name, so `reprobot serve` then stays on
// loopback. See docs/SERVICE.md.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/spec"
	"github.com/DCchoudhury15/reprobot/internal/store"
)

// DefaultMaxBodyBytes is the default limit for one upload.
const DefaultMaxBodyBytes = 60 << 20

const (
	reproFile        = "repro.yaml"
	maxReproBytes    = 1 << 20
	maxFiles         = 100
	maxFileNameBytes = 255
	maxReporterBytes = 256
	maxKeyBytes      = 255
)

var projectID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Server serves the API.
type Server struct {
	Store        *store.Store
	DataDir      string  // reports go to DataDir/reports/<id>
	ProjectsDir  string  // holds <project>.yaml files
	MaxBodyBytes int64   // 0 means DefaultMaxBodyBytes
	Tokens       *Tokens // nil means no authentication
	// ReportsPerHour caps the reports one token may send in any hour; 0
	// means no limit. Without Tokens there is no limit.
	ReportsPerHour int
	// SigningKeyPEM is the public key the workers sign results with,
	// served at /v1/signing-key. nil means none is published.
	SigningKeyPEM []byte
	// GitHub, when set, takes private vulnerability reports from a GitHub
	// App at POST /v1/github/webhook.
	GitHub *GitHubIntake
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	protect := func(h http.HandlerFunc) http.Handler {
		if s.Tokens == nil {
			return h
		}
		return s.requireToken(h)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/reports", protect(s.createReport))
	mux.Handle("GET /v1/reports/{id}", protect(s.reportJSON))
	mux.Handle("GET /v1/reports/{id}/signed", protect(s.reportSigned))
	mux.Handle("GET /v1/reports/{id}/fix/signed", protect(s.fixSigned))
	mux.Handle("GET /v1/reports/{id}/bisect/signed", protect(s.bisectSigned))
	mux.Handle("GET /reports/{id}", protect(s.reportPage))
	mux.HandleFunc("GET /v1/signing-key", s.signingKey)
	mux.HandleFunc("POST /v1/github/webhook", s.githubWebhook)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

func (s *Server) maxBody() int64 {
	if s.MaxBodyBytes > 0 {
		return s.MaxBodyBytes
	}
	return DefaultMaxBodyBytes
}

// httpError is an error with the status code to send.
type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return &httpError{code: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var he *httpError
	var mbe *http.MaxBytesError
	var rle *store.RateLimitError
	switch {
	case errors.As(err, &rle):
		secs := int((rle.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, http.StatusTooManyRequests,
			map[string]string{"error": fmt.Sprintf("too many reports from this token; retry in %d seconds", secs)})
	case errors.As(err, &mbe):
		writeJSON(w, http.StatusRequestEntityTooLarge,
			map[string]string{"error": fmt.Sprintf("request body is larger than %d bytes", mbe.Limit)})
	case errors.As(err, &he):
		writeJSON(w, he.code, map[string]string{"error": he.msg})
	default:
		slog.Error("request failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

type createResponse struct {
	ReportID  string `json:"report_id"`
	JobID     int64  `json:"job_id"`
	StatusURL string `json:"status_url"`
}

func (s *Server) createReport(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if err := checkText("Idempotency-Key", key, maxKeyBytes); err != nil {
		writeError(w, err)
		return
	}
	// The GitHub webhook uses "github:<GHSA id>" keys for reports without
	// an owner. Another upload must not take one first.
	if strings.HasPrefix(key, "github:") {
		writeError(w, badRequest("Idempotency-Key may not start with github:"))
		return
	}
	var owner string
	if c := callerFrom(r.Context()); c != nil {
		owner = c.name
	}
	// Refuse before reading the upload. A request with a key may repeat
	// an earlier one, which does not count, so it goes on to the exact
	// check in CreateReport.
	if key == "" {
		if err := s.Store.CheckRate(r.Context(), owner, s.ReportsPerHour); err != nil {
			writeError(w, err)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBody())
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, badRequest("expected a multipart/form-data body: %v", err))
		return
	}

	// Files go to a temp dir first. It is renamed into place only when the
	// whole upload is valid, so a failed upload leaves nothing behind.
	tmp, err := s.uploadDir()
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.RemoveAll(tmp)

	up, err := readUpload(mr, tmp)
	if err != nil {
		writeError(w, err)
		return
	}
	resp, created, err := s.finishReport(r.Context(), tmp, up, owner, key, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// finishReport checks the upload in tmp (repro.yaml and the files, already
// written), moves it into place and queues the report. created is false
// when the idempotency key names an earlier report, which is returned.
func (s *Server) finishReport(ctx context.Context, tmp string, up *upload, owner, key string, gh *store.GitHubRef) (resp createResponse, created bool, err error) {
	up.hash = reproducerHash(tmp)
	project, err := parseProject(up.repro)
	if err != nil {
		return resp, false, err
	}
	if err := s.checkProject(project); err != nil {
		return resp, false, err
	}
	if up.wantProject != "" && project != up.wantProject {
		return resp, false, badRequest("repro.yaml names project %q, but this repository is project %q", project, up.wantProject)
	}

	id := uuid.New()
	dir, err := filepath.Abs(filepath.Join(s.DataDir, "reports", id.String()))
	if err != nil {
		return resp, false, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return resp, false, err
	}
	reportID, jobID, created, err := s.Store.CreateReport(ctx, store.ReportInput{
		ID:             id,
		Project:        project,
		Reporter:       up.reporter,
		Owner:          owner,
		IdempotencyKey: key,
		MaxPerHour:     s.ReportsPerHour,
		ReproHash:      up.hash,
		Dir:            dir,
		FixCommit:      up.fixCommit,
		GoodCommit:     up.goodCommit,
		GitHub:         gh,
	})
	if err != nil || !created {
		// Nothing points at the new directory.
		os.RemoveAll(dir)
	}
	if err != nil {
		return resp, false, err
	}
	return createResponse{
		ReportID:  reportID.String(),
		JobID:     jobID,
		StatusURL: "/v1/reports/" + reportID.String(),
	}, created, nil
}

type upload struct {
	repro     []byte
	reporter  string
	fixCommit string
	// goodCommit asks for a bisect from this commit.
	goodCommit string
	hash       string
	// wantProject, when set, is the project the report must be for.
	wantProject string
}

// readUpload reads the multipart body and writes repro.yaml and the
// reporter files into dir.
func readUpload(mr *multipart.Reader, dir string) (*upload, error) {
	up := &upload{}
	seen := map[string]bool{}
	haveRepro, haveReporter := false, false
	nfiles := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, wrapRead(err)
		}
		switch part.FormName() {
		case "repro":
			if haveRepro {
				return nil, badRequest("more than one repro field")
			}
			haveRepro = true
			data, err := io.ReadAll(io.LimitReader(part, maxReproBytes+1))
			if err != nil {
				return nil, wrapRead(err)
			}
			if len(data) > maxReproBytes {
				return nil, badRequest("repro.yaml is larger than %d bytes", maxReproBytes)
			}
			if err := os.WriteFile(filepath.Join(dir, reproFile), data, 0o600); err != nil {
				return nil, err
			}
			up.repro = data
		case "file":
			name, err := rawFileName(part)
			if err != nil {
				return nil, err
			}
			if err := checkFileName(name); err != nil {
				return nil, err
			}
			if seen[name] {
				return nil, badRequest("file %q sent twice", name)
			}
			if nfiles++; nfiles > maxFiles {
				return nil, badRequest("more than %d files", maxFiles)
			}
			if err := writeFile(filepath.Join(dir, name), part); err != nil {
				return nil, err
			}
			seen[name] = true
		case "reporter":
			if haveReporter {
				return nil, badRequest("more than one reporter field")
			}
			haveReporter = true
			data, err := io.ReadAll(io.LimitReader(part, maxReporterBytes+1))
			if err != nil {
				return nil, wrapRead(err)
			}
			v := strings.TrimSpace(string(data))
			if err := checkText("reporter", v, maxReporterBytes); err != nil {
				return nil, err
			}
			up.reporter = v
		case "fix_commit":
			if err := readCommit(part, &up.fixCommit); err != nil {
				return nil, err
			}
		case "good_commit":
			if err := readCommit(part, &up.goodCommit); err != nil {
				return nil, err
			}
		default:
			return nil, badRequest("unknown form field %q", part.FormName())
		}
		part.Close()
	}
	if !haveRepro {
		return nil, badRequest("missing repro field")
	}
	return up, nil
}

// wrapRead keeps a too-large body as is (413) and turns other read errors
// into a bad request.
func wrapRead(err error) error {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return err
	}
	return badRequest("bad multipart body: %v", err)
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// Write and close errors are local problems; anything else came
		// from reading the request.
		var pe *os.PathError
		if errors.As(err, &pe) {
			return err
		}
		return wrapRead(err)
	}
	return nil
}

// rawFileName returns the filename parameter as sent. Part.FileName is not
// used because it strips directories, which would hide names like "../x".
func rawFileName(p *multipart.Part) (string, error) {
	_, params, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
	if err != nil {
		return "", badRequest("bad Content-Disposition: %v", err)
	}
	return params["filename"], nil
}

// checkFileName allows only plain names: files land flat in the report
// directory next to repro.yaml.
func checkFileName(name string) error {
	switch {
	case name == "":
		return badRequest("file without a name")
	case strings.ContainsAny(name, `/\`):
		return badRequest("file name %q contains a path separator", name)
	case strings.HasPrefix(name, "."):
		return badRequest("file name %q starts with a dot", name)
	case name == reproFile:
		return badRequest("file name %q is reserved", name)
	}
	return checkText("file name", name, maxFileNameBytes)
}

// truncateText cuts v to at most n runes, for error messages.
func truncateText(v string, n int) string {
	if r := []rune(v); len(r) > n {
		return string(r[:n]) + "..."
	}
	return v
}

// checkText rejects values that are too long, not UTF-8, or contain
// control characters.
func checkText(what, v string, maxBytes int) error {
	if len(v) > maxBytes {
		return badRequest("%s is longer than %d bytes", what, maxBytes)
	}
	if !utf8.ValidString(v) {
		return badRequest("%s is not valid UTF-8", what)
	}
	if strings.ContainsFunc(v, unicode.IsControl) {
		return badRequest("%s contains control characters", what)
	}
	return nil
}

// parseProject reads only the project field. The runner validates the full
// file later.
func parseProject(repro []byte) (string, error) {
	var head struct {
		Project string `yaml:"project"`
	}
	if err := yaml.Unmarshal(repro, &head); err != nil {
		return "", badRequest("repro.yaml: %v", err)
	}
	if !projectID.MatchString(head.Project) {
		return "", badRequest("repro.yaml: project %q is not a valid project id", head.Project)
	}
	return head.Project, nil
}

func (s *Server) checkProject(project string) error {
	fi, err := os.Stat(filepath.Join(s.ProjectsDir, project+".yaml"))
	if err != nil || !fi.Mode().IsRegular() {
		return badRequest("unknown project %q", project)
	}
	return nil
}

// reproducerHash returns the same hash the runner puts in
// result.inputs.reproducer_hash, so a report shows one hash before and
// after it runs. It is empty when repro.yaml is not valid or a listed file
// is missing; the runner then gives INVALID_INPUT with the reason. Uploaded
// files that repro.yaml does not list are never mounted, so they are not
// part of the hash.
func reproducerHash(dir string) string {
	path := filepath.Join(dir, reproFile)
	r, err := spec.LoadReproducer(path)
	if err != nil {
		return ""
	}
	h, err := spec.ReproducerHash(path, r)
	if err != nil {
		return ""
	}
	return h
}

// readCommit reads a commit form field (fix_commit, good_commit) into
// *dst, which must still be empty.
func readCommit(part *multipart.Part, dst *string) error {
	name := part.FormName()
	if *dst != "" {
		return badRequest("more than one %s field", name)
	}
	data, err := io.ReadAll(io.LimitReader(part, maxKeyBytes+1))
	if err != nil {
		return wrapRead(err)
	}
	v := strings.TrimSpace(string(data))
	if !spec.ValidCommit(v) {
		return badRequest("%s %q is not a commit hash or tag name", name, truncateText(v, 80))
	}
	*dst = v
	return nil
}

// reportView is what both the JSON endpoint and the page show.
type reportView struct {
	ReportID    string         `json:"report_id"`
	Project     string         `json:"project"`
	Reporter    string         `json:"reporter,omitempty"`
	ReproHash   string         `json:"repro_hash,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	JobID       int64          `json:"job_id"`
	State       string         `json:"state"`
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	LastError   string         `json:"last_error,omitempty"`
	Result      *result.Result `json:"result,omitempty"`
	Duplicates  []string       `json:"duplicates"`
	// Related reports share the signature but crash on another line.
	Related []string `json:"related"`
	// Signed is true when a signed result is at /v1/reports/{id}/signed.
	Signed bool `json:"signed"`
	// Fix is the fix check; nil when the report asked for none.
	Fix *fixView `json:"fix,omitempty"`
	// Bisect is the bisect; nil when the report asked for none.
	Bisect         *bisectView `json:"bisect,omitempty"`
	envelope       []byte      // the signed result, byte for byte
	fixEnvelope    []byte      // the signed fix check, byte for byte
	bisectEnvelope []byte      // the signed bisect, byte for byte
}

// bisectView is the bisect of a report. State is empty until the job is
// done.
type bisectView struct {
	GoodCommit string              `json:"good_commit"`
	State      result.BisectState  `json:"state,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	FirstBad   string              `json:"first_bad,omitempty"`
	LastGood   string              `json:"last_good,omitempty"`
	Untested   int                 `json:"untested,omitempty"`
	Commits    int                 `json:"commits,omitempty"`
	Steps      []result.BisectStep `json:"steps,omitempty"`
	// Signed is true when a signed bisect is at
	// /v1/reports/{id}/bisect/signed.
	Signed bool `json:"signed"`
}

// fixView is the fix check of a report. State is empty until the job is
// done. The report run is Result, so it is not repeated here.
type fixView struct {
	Commit string          `json:"commit"`
	State  result.FixState `json:"state,omitempty"`
	Reason string          `json:"reason,omitempty"`
	Run    *result.Result  `json:"run,omitempty"` // on the fix commit
	// Signed is true when a signed fix check is at
	// /v1/reports/{id}/fix/signed.
	Signed bool `json:"signed"`
}

// loadReport returns nil, nil when the id is malformed or unknown, or the
// caller may not read the report: both look the same, so a token cannot
// probe which ids exist. The duplicate lists hold only reports the caller
// may read.
func (s *Server) loadReport(ctx context.Context, rawID string) (*reportView, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, nil
	}
	rep, err := s.Store.GetReport(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c := callerFrom(ctx)
	if !c.canRead(rep.Project, rep.Owner) {
		return nil, nil
	}
	// Every report in the lists is of rep.Project. A caller who cannot read
	// that whole project sees only its own reports there.
	var onlyOwner *string
	if !c.canRead(rep.Project, "") {
		onlyOwner = &c.name
	}
	v := &reportView{
		ReportID:    rep.ID.String(),
		Project:     rep.Project,
		Reporter:    rep.Reporter,
		ReproHash:   rep.ReproHash,
		CreatedAt:   rep.CreatedAt,
		JobID:       rep.JobID,
		State:       rep.JobState,
		Attempts:    rep.Attempts,
		MaxAttempts: rep.MaxAttempts,
		LastError:   rep.LastError,
		Result:      rep.Result,
		Duplicates:  []string{},
		Related:     []string{},
		Signed:      rep.Envelope != nil,
		envelope:    rep.Envelope,
		fixEnvelope: rep.FixEnvelope,

		bisectEnvelope: rep.BisectEnvelope,
	}
	if rep.FixCommit != "" {
		v.Fix = &fixView{Commit: rep.FixCommit, Signed: rep.FixEnvelope != nil}
		if f := rep.Fix; f != nil {
			v.Fix.State, v.Fix.Reason, v.Fix.Run = f.Fix, f.Reason, f.FixRun
		}
	}
	if rep.GoodCommit != "" {
		v.Bisect = &bisectView{GoodCommit: rep.GoodCommit, Signed: rep.BisectEnvelope != nil}
		if b := rep.Bisect; b != nil {
			v.Bisect.State, v.Bisect.Reason = b.Bisect, b.Reason
			v.Bisect.FirstBad, v.Bisect.LastGood, v.Bisect.Untested = b.FirstBad, b.LastGood, b.Untested
			v.Bisect.Commits, v.Bisect.Steps = b.Commits, b.Steps
		}
	}
	dups, related, err := s.Store.Duplicates(ctx, rep.Project, rep.Signature, rep.Site, rep.ID, onlyOwner)
	if err != nil {
		return nil, err
	}
	for _, d := range dups {
		v.Duplicates = append(v.Duplicates, d.String())
	}
	for _, d := range related {
		v.Related = append(v.Related, d.String())
	}
	return v, nil
}

func (s *Server) reportJSON(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadReport(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if v == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// reportSigned serves the signed result exactly as the worker signed it.
func (s *Server) reportSigned(w http.ResponseWriter, r *http.Request) {
	s.serveEnvelope(w, r, "result", func(v *reportView) []byte { return v.envelope })
}

// fixSigned serves the signed fix check exactly as the worker signed it.
func (s *Server) fixSigned(w http.ResponseWriter, r *http.Request) {
	s.serveEnvelope(w, r, "fix check", func(v *reportView) []byte { return v.fixEnvelope })
}

// bisectSigned serves the signed bisect exactly as the worker signed it.
func (s *Server) bisectSigned(w http.ResponseWriter, r *http.Request) {
	s.serveEnvelope(w, r, "bisect", func(v *reportView) []byte { return v.bisectEnvelope })
}

func (s *Server) serveEnvelope(w http.ResponseWriter, r *http.Request, what string, pick func(*reportView) []byte) {
	v, err := s.loadReport(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if v == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "report not found"})
		return
	}
	env := pick(v)
	if env == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this report has no signed " + what})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(env)
}

// signingKey serves the public key results are signed with. It is public,
// so it needs no token.
func (s *Server) signingKey(w http.ResponseWriter, r *http.Request) {
	if s.SigningKeyPEM == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this server publishes no signing key"})
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(s.SigningKeyPEM)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := s.Store.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "database unavailable")
		return
	}
	fmt.Fprintln(w, "ok")
}
