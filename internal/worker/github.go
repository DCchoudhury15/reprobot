package worker

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/github"
	"github.com/DCchoudhury15/reprobot/internal/store"
)

// Commenter posts a comment on an advisory. *github.App has this method.
type Commenter interface {
	Comment(ctx context.Context, installation int64, repo, ghsa, body string) error
}

// Notifier posts the verdict of each finished GitHub report as a comment
// on its advisory. The queue is in the database, so a failed post (GitHub
// down, a bad token) is tried again with a growing wait, and a restart
// loses nothing. A comment that was posted but not recorded (the process
// died in between) is posted twice; one that never posts is not lost.
type Notifier struct {
	Store        *store.Store
	GitHub       Commenter
	PublicURL    string        // base URL of the result pages; empty means no link
	PollInterval time.Duration // default 5s
}

// commentHold is how long a claimed comment is held before another
// notifier may take it.
const commentHold = 2 * time.Minute

// Serve posts comments until ctx is cancelled.
func (n *Notifier) Serve(ctx context.Context) {
	poll := n.PollInterval
	if poll <= 0 {
		poll = 5 * time.Second
	}
	for ctx.Err() == nil {
		if !n.Once(ctx) {
			sleep(ctx, poll)
		}
	}
}

// Once posts at most one due comment. It returns false when none was due or
// the queue could not be read.
func (n *Notifier) Once(ctx context.Context) bool {
	c, err := n.Store.ClaimGitHubComment(ctx, commentHold)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("claim github comment", "err", err)
		}
		return false
	}
	if c == nil {
		return false
	}
	log := slog.With("report", c.ReportID, "advisory", c.Ref.GHSA, "repo", c.Ref.Repo, "attempt", c.Attempts)
	link := ""
	if n.PublicURL != "" {
		link = strings.TrimRight(n.PublicURL, "/") + "/reports/" + c.ReportID.String()
	}
	body := github.CommentBody(c.Result, c.Fix, link)
	pctx, cancel := context.WithTimeout(ctx, time.Minute)
	err = n.GitHub.Comment(pctx, c.Ref.Installation, c.Ref.Repo, c.Ref.GHSA, body)
	cancel()
	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer scancel()
	if err != nil {
		retry := commentBackoff(c.Attempts)
		if c.Attempts >= store.MaxGitHubCommentAttempts {
			log.Error("github comment failed for good", "err", err)
		} else {
			log.Warn("github comment failed", "err", err, "retry_in", retry)
		}
		if serr := n.Store.GitHubCommentFailed(sctx, c.ReportID, retry, err.Error()); serr != nil {
			log.Error("record github comment failure", "err", serr)
		}
		return true
	}
	if err := n.Store.GitHubCommented(sctx, c.ReportID); err != nil {
		log.Error("record github comment", "err", err)
	}
	log.Info("github comment posted")
	return true
}

// commentBackoff is 30s * 2^(attempts-1), at most an hour.
func commentBackoff(attempts int) time.Duration {
	d := 30 * time.Second
	for i := 1; i < attempts && d < time.Hour; i++ {
		d *= 2
	}
	return min(d, time.Hour)
}
