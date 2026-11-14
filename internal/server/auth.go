package server

import (
	"bufio"
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
// File format: one "NAME SHA256HEX" pair per line. Blank lines and lines
// that start with # are skipped. `reprobot token NAME` prints a new token
// and its line.
type Tokens struct {
	names []string
	sums  [][sha256.Size]byte
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
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want NAME SHA256HEX", path, n)
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

// check returns the name of the token, if it is one of t. Every entry is
// compared, in constant time, so the timing does not show which matched.
func (t *Tokens) check(token string) (string, bool) {
	if token == "" || len(token) > maxTokenBytes {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	found := -1
	for i := range t.sums {
		if subtle.ConstantTimeCompare(sum[:], t.sums[i][:]) == 1 {
			found = i
		}
	}
	if found < 0 {
		return "", false
	}
	return t.names[found], true
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
		name, ok := s.Tokens.check(presentedToken(r))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="reprobot", charset="UTF-8"`)
			w.Header().Add("WWW-Authenticate", `Bearer realm="reprobot"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a valid API token is required"})
			return
		}
		slog.Debug("authenticated", "token", name, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
