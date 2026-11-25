package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/github"
	"github.com/DCchoudhury15/reprobot/internal/spec"
	"github.com/DCchoudhury15/reprobot/internal/store"
)

// GitHubIntake takes private vulnerability reports from a GitHub App
// webhook. See docs/SERVICE.md.
type GitHubIntake struct {
	// WebhookSecret is the App's webhook secret. Deliveries without a
	// valid X-Hub-Signature-256 are refused.
	WebhookSecret []byte
	// App, when set, posts a comment on a report whose reprobot block
	// cannot be taken in, so the reporter learns why.
	App *github.App
}

// maxWebhookBytes caps one delivery. An advisory description is at most
// 65535 characters, so real deliveries are far smaller.
const maxWebhookBytes = 5 << 20

// rejectTimeout bounds the comment on a rejected report. GitHub waits 10
// seconds for the webhook answer.
const rejectTimeout = 8 * time.Second

// webhookAnswer is the body of every 2xx webhook answer.
type webhookAnswer struct {
	Status string `json:"status"` // pong, ignored, rejected, queued, duplicate
	Reason string `json:"reason,omitempty"`
	*createResponse
}

// githubWebhook handles POST /v1/github/webhook. It needs no API token: the
// webhook secret signs each delivery. Reports it cannot use get a 2xx
// answer, because the delivery itself was fine.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	g := s.GitHub
	if g == nil || len(g.WebhookSecret) == 0 {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBytes+1))
	if err != nil {
		writeError(w, badRequest("read body: %v", err))
		return
	}
	if len(body) > maxWebhookBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "delivery is too large"})
		return
	}
	if !github.VerifySignature(g.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad or missing X-Hub-Signature-256"})
		return
	}
	ignore := func(reason string) {
		writeJSON(w, http.StatusAccepted, webhookAnswer{Status: "ignored", Reason: reason})
	}
	switch ev := r.Header.Get("X-GitHub-Event"); ev {
	case github.EventPing:
		writeJSON(w, http.StatusOK, webhookAnswer{Status: "pong"})
		return
	case github.EventAdvisory:
	default:
		ignore("event " + truncateText(ev, 60) + " is not used")
		return
	}
	ev, err := github.ParseAdvisoryEvent(body)
	if err != nil {
		writeError(w, badRequest("%v", err))
		return
	}
	if ev.Action != github.ActionReported {
		ignore("action " + truncateText(ev.Action, 60) + " is not used")
		return
	}
	log := slog.With("advisory", ev.Advisory.GHSAID, "repo", ev.Repository.FullName, "delivery", r.Header.Get("X-GitHub-Delivery"))
	project, err := s.projectForRepo(ev.Repository.FullName)
	if err != nil {
		log.Info("github report ignored", "reason", err)
		ignore(err.Error())
		return
	}
	sub, err := github.Extract(ev.Advisory.Description)
	if errors.Is(err, github.ErrNoBlock) {
		ignore("the report has no reprobot block")
		return
	}
	reject := func(reason string) {
		log.Info("github report rejected", "reason", reason)
		if g.App != nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), rejectTimeout)
			defer cancel()
			if err := g.App.Comment(ctx, ev.Installation.ID, ev.Repository.FullName, ev.Advisory.GHSAID, github.RejectBody(reason)); err != nil {
				log.Warn("github comment on rejected report", "err", err)
			}
		}
		writeJSON(w, http.StatusAccepted, webhookAnswer{Status: "rejected", Reason: reason})
	}
	if err != nil {
		reject(err.Error())
		return
	}

	tmp, err := s.uploadDir()
	if err != nil {
		writeError(w, err)
		return
	}
	defer os.RemoveAll(tmp)
	up, err := writeSubmission(tmp, sub)
	if err != nil {
		reject(err.Error())
		return
	}
	up.wantProject = project
	up.reporter = "github"
	if login := ev.Sender.Login; login != "" && checkText("reporter", login, 100) == nil {
		up.reporter = "github:" + login
	}
	ref := &store.GitHubRef{Installation: ev.Installation.ID, Repo: ev.Repository.FullName, GHSA: ev.Advisory.GHSAID}
	// No owner: only maintainer tokens read these reports. The advisory id
	// is the idempotency key, so a redelivery finds the first report.
	resp, created, err := s.finishReport(r.Context(), tmp, up, "", "github:"+ev.Advisory.GHSAID, ref)
	var he *httpError
	if errors.As(err, &he) {
		reject(he.msg)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	status, code := "queued", http.StatusAccepted
	if !created {
		status, code = "duplicate", http.StatusOK
	}
	log.Info("github report "+status, "report", resp.ReportID)
	writeJSON(w, code, webhookAnswer{Status: status, createResponse: &resp})
}

// uploadDir makes a temp dir for one upload under DataDir. It is renamed
// into DataDir/reports only when the whole upload is valid.
func (s *Server) uploadDir() (string, error) {
	tmpRoot := filepath.Join(s.DataDir, "tmp")
	for _, d := range []string{filepath.Join(s.DataDir, "reports"), tmpRoot} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(tmpRoot, "upload-")
}

// writeSubmission writes repro.yaml and the files of a GitHub report into
// dir, with the same checks as a multipart upload.
func writeSubmission(dir string, sub *github.Submission) (*upload, error) {
	if len(sub.Repro) > maxReproBytes {
		return nil, badRequest("repro.yaml is larger than %d bytes", maxReproBytes)
	}
	if len(sub.Files) > maxFiles {
		return nil, badRequest("more than %d files", maxFiles)
	}
	if err := os.WriteFile(filepath.Join(dir, reproFile), sub.Repro, 0o600); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range sub.Files {
		if err := checkFileName(f.Name); err != nil {
			return nil, err
		}
		if seen[f.Name] {
			return nil, badRequest("file %q sent twice", f.Name)
		}
		seen[f.Name] = true
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0o600); err != nil {
			return nil, err
		}
	}
	return &upload{repro: sub.Repro}, nil
}

// projectForRepo finds the one project whose repo is the GitHub repository
// owner/name.
func (s *Server) projectForRepo(fullName string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(s.ProjectsDir, "*.yaml"))
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	var found []string
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if !projectID.MatchString(id) {
			continue
		}
		proj, err := spec.LoadProject(p)
		if err != nil {
			continue
		}
		if repo, ok := githubRepo(proj.Repo); ok && strings.EqualFold(repo, fullName) {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		return "", errors.New("no project is set up for " + fullName)
	case 1:
		return found[0], nil
	default:
		return "", errors.New("more than one project is set up for " + fullName + ": " + strings.Join(found, ", "))
	}
}

// githubRepo returns owner/name for a github.com clone URL.
func githubRepo(url string) (string, bool) {
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(url, prefix); ok {
			rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
			return rest, github.ValidRepo(rest)
		}
	}
	return "", false
}
