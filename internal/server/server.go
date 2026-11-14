// Package server is the HTTP intake API and the result pages.
//
// There is no authentication in v0. Anyone who can reach the address can
// submit reports and read every result and reporter name. Run it on
// 127.0.0.1 (the default of `reprobot serve`) or behind a proxy that
// authenticates users. See docs/SERVICE.md.
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

// Server serves the API. It has no authentication; see the package doc.
type Server struct {
	Store        *store.Store
	DataDir      string // reports go to DataDir/reports/<id>
	ProjectsDir  string // holds <project>.yaml files
	MaxBodyBytes int64  // 0 means DefaultMaxBodyBytes
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/reports", s.createReport)
	mux.HandleFunc("GET /v1/reports/{id}", s.reportJSON)
	mux.HandleFunc("GET /reports/{id}", s.reportPage)
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
	switch {
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
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBody())
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, badRequest("expected a multipart/form-data body: %v", err))
		return
	}

	reportsDir := filepath.Join(s.DataDir, "reports")
	tmpRoot := filepath.Join(s.DataDir, "tmp")
	for _, d := range []string{reportsDir, tmpRoot} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			writeError(w, err)
			return
		}
	}
	// Files go to a temp dir first. It is renamed into place only when the
	// whole upload is valid, so a failed upload leaves nothing behind.
	tmp, err := os.MkdirTemp(tmpRoot, "upload-")
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
	up.hash = reproducerHash(tmp)
	project, err := parseProject(up.repro)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := s.checkProject(project); err != nil {
		writeError(w, err)
		return
	}

	id := uuid.New()
	dir, err := filepath.Abs(filepath.Join(reportsDir, id.String()))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := os.Rename(tmp, dir); err != nil {
		writeError(w, err)
		return
	}
	reportID, jobID, created, err := s.Store.CreateReport(r.Context(), store.ReportInput{
		ID:             id,
		Project:        project,
		Reporter:       up.reporter,
		IdempotencyKey: key,
		ReproHash:      up.hash,
		Dir:            dir,
	})
	if err != nil || !created {
		// Nothing points at the new directory.
		os.RemoveAll(dir)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	resp := createResponse{
		ReportID:  reportID.String(),
		JobID:     jobID,
		StatusURL: "/v1/reports/" + reportID.String(),
	}
	if !created {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	writeJSON(w, http.StatusAccepted, resp)
}

type upload struct {
	repro    []byte
	reporter string
	hash     string
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
}

// loadReport returns nil, nil when the id is malformed or unknown.
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
	}
	dups, related, err := s.Store.Duplicates(ctx, rep.Project, rep.Signature, rep.Site, rep.ID)
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
