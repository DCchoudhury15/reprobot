// Package github takes private vulnerability reports from a GitHub App and
// posts reprobot's verdict back to them.
//
// GitHub sends a repository_advisory webhook with action "reported" when
// someone files a private vulnerability report. The report text (the
// advisory description) can hold a reprobot block: the repro.yaml in a
// ```reprobot fence and each reporter file in a ```reprobot-file NAME fence.
// The verdict goes back as a comment on the advisory.
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Event and action names.
const (
	EventAdvisory  = "repository_advisory"
	EventPing      = "ping"
	ActionReported = "reported"
)

// VerifySignature checks the X-Hub-Signature-256 header: "sha256=" and the
// hex HMAC-SHA256 of the raw body, keyed with the webhook secret.
func VerifySignature(secret, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign returns the X-Hub-Signature-256 value for body. Tests and tools use
// it to send deliveries like GitHub does.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// AdvisoryEvent is the part of a repository_advisory delivery reprobot uses.
type AdvisoryEvent struct {
	Action   string `json:"action"`
	Advisory struct {
		GHSAID      string `json:"ghsa_id"`
		HTMLURL     string `json:"html_url"`
		Summary     string `json:"summary"`
		Description string `json:"description"`
		State       string `json:"state"`
	} `json:"repository_advisory"`
	Repository struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

var (
	ghsaRe = regexp.MustCompile(`^GHSA(-[23456789cfghjmpqrvwx]{4}){3}$`)
	// GitHub owner and repository names. The dot-only names are refused
	// so a name can never step out of the API path.
	repoRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)
)

// ValidGHSA says whether s is a GHSA id.
func ValidGHSA(s string) bool { return ghsaRe.MatchString(s) }

// ValidRepo says whether s is an "owner/name" repository name that is safe
// to put in an API path.
func ValidRepo(s string) bool {
	if !repoRe.MatchString(s) {
		return false
	}
	_, name, _ := strings.Cut(s, "/")
	return name != "." && name != ".."
}

// ParseAdvisoryEvent decodes a repository_advisory delivery and checks the
// fields reprobot puts in paths and in the database.
func ParseAdvisoryEvent(body []byte) (*AdvisoryEvent, error) {
	var ev AdvisoryEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("bad repository_advisory payload: %w", err)
	}
	if ev.Action != ActionReported {
		return &ev, nil
	}
	switch {
	case !ValidGHSA(ev.Advisory.GHSAID):
		return nil, fmt.Errorf("bad ghsa_id %q", truncate(ev.Advisory.GHSAID, 40))
	case !ValidRepo(ev.Repository.FullName):
		return nil, fmt.Errorf("bad repository full_name %q", truncate(ev.Repository.FullName, 150))
	case ev.Installation.ID <= 0:
		return nil, errors.New("the delivery has no installation id; is it from a GitHub App?")
	}
	return &ev, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
