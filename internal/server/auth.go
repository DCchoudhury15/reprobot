package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Tokens are the API tokens a server accepts. Only the sha256 of each token
// is kept, so the tokens file holds no secrets.
//
// File format: one "NAME SHA256HEX [PROJECTS]" line per token. Blank lines
// and lines that start with # are skipped. `reprobot token NAME` prints a
// new token and its line.
//
// PROJECTS makes the token a maintainer token: a comma separated list of
// project ids whose reports it may read, or "*" for every project. A token
// without it is a reporter token and reads only the reports it submitted.
// Any token may submit a report for any project.
type Tokens struct {
	names []string
	sums  [][sha256.Size]byte
	reads []projectSet
}

// projectSet is the projects a token reads every report of.
type projectSet struct {
	all bool
	ids map[string]bool
}

func parseProjects(field string) (projectSet, error) {
	if field == "*" {
		return projectSet{all: true}, nil
	}
	ps := projectSet{ids: map[string]bool{}}
	for _, id := range strings.Split(field, ",") {
		if !projectID.MatchString(id) {
			return projectSet{}, fmt.Errorf("bad project id %q in the projects list", id)
		}
		ps.ids[id] = true
	}
	return ps, nil
}

// caller is who sent a request. A nil *caller means the server runs
// without tokens, and everyone reads everything.
type caller struct {
	name  string
	reads projectSet
}

// canRead reports whether c may read a report of project submitted by
// owner (empty when it was submitted without a token; token names are
// never empty, so that matches no reporter).
func (c *caller) canRead(project, owner string) bool {
	if c == nil {
		return true
	}
	return owner == c.name || c.reads.all || c.reads.ids[project]
}

type callerKey struct{}

func callerFrom(ctx context.Context) *caller {
	c, _ := ctx.Value(callerKey{}).(*caller)
	return c
}

var tokenName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

// maxTokenBytes caps a presented token; real ones are 43 bytes.
const maxTokenBytes = 256

// LoadTokens reads a tokens file. A file with no tokens is an error, so a
// typo cannot turn authentication into "nobody may enter" by surprise.
func LoadTokens(path string) (*Tokens, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &Tokens{}
	seenName := map[string]bool{}
	seenSum := map[[sha256.Size]byte]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 && len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: want NAME SHA256HEX [PROJECTS]", path, n)
		}
		var reads projectSet
		if len(fields) == 3 {
			if reads, err = parseProjects(fields[2]); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, n, err)
			}
		}
		name, hexSum := fields[0], fields[1]
		if !tokenName.MatchString(name) {
			return nil, fmt.Errorf("%s:%d: bad token name %q", path, n, name)
		}
		raw, err := hex.DecodeString(hexSum)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("%s:%d: token hash must be 64 hex characters", path, n)
		}
		var sum [sha256.Size]byte
		copy(sum[:], raw)
		if seenName[name] {
			return nil, fmt.Errorf("%s:%d: token name %q is listed twice", path, n, name)
		}
		if seenSum[sum] {
			return nil, fmt.Errorf("%s:%d: the same token is listed twice", path, n)
		}
		seenName[name], seenSum[sum] = true, true
		t.names = append(t.names, name)
		t.sums = append(t.sums, sum)
		t.reads = append(t.reads, reads)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(t.names) == 0 {
		return nil, fmt.Errorf("%s: no tokens", path)
	}
	return t, nil
}

// NewToken returns a new random token and its line for the tokens file.
func NewToken(name string) (token, line string, err error) {
	if !tokenName.MatchString(name) {
		return "", "", fmt.Errorf("bad token name %q: use letters, digits, '.', '_', '@' or '-', at most 64", name)
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	return token, name + " " + hex.EncodeToString(sum[:]), nil
}

// check returns who holds the token, if it is one of t. Every entry is
// compared, in constant time, so the timing does not show which matched.
func (t *Tokens) check(token string) (*caller, bool) {
	if token == "" || len(token) > maxTokenBytes {
		return nil, false
	}
	sum := sha256.Sum256([]byte(token))
	found := -1
	for i := range t.sums {
		if subtle.ConstantTimeCompare(sum[:], t.sums[i][:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return nil, false
	}
	return &caller{name: t.names[found], reads: t.reads[found]}, true
}

// presentedToken reads "Authorization: Bearer TOKEN", or Basic auth with
// the token as the password (any user name), which lets a browser open the
// result pages.
func presentedToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if scheme, rest, ok := strings.Cut(h, " "); ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(rest)
	}
	if _, pass, ok := r.BasicAuth(); ok {
		return pass
	}
	return ""
}

// requireToken rejects requests without a valid token before the handler
// reads the body.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := s.Tokens.check(presentedToken(r))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="reprobot", charset="UTF-8"`)
			w.Header().Add("WWW-Authenticate", `Bearer realm="reprobot"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a valid API token is required"})
			return
		}
		slog.Debug("authenticated", "token", c.name, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, c)))
	})
}
