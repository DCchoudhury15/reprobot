package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the GitHub REST API.
const DefaultBaseURL = "https://api.github.com"

// App talks to the GitHub API as a GitHub App installation. It needs the
// "Repository security advisories" permission: read to get the
// repository_advisory webhook, write to comment on advisories.
type App struct {
	ID      string          // the App's client ID or app ID, the JWT issuer
	Key     *rsa.PrivateKey // the App's private key
	BaseURL string          // default DefaultBaseURL
	HTTP    *http.Client    // default a client with a 30s timeout
	Now     func() time.Time

	mu     sync.Mutex
	tokens map[int64]instToken
}

type instToken struct {
	token   string
	expires time.Time
}

// ParseKey reads the App private key, PKCS#1 ("RSA PRIVATE KEY", what
// GitHub hands out) or PKCS#8.
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github app key: no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github app key: not an RSA key")
	}
	return rk, nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) base() string {
	if a.BaseURL != "" {
		return strings.TrimRight(a.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (a *App) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// JWT returns the App's JSON Web Token: RS256, issued 60 seconds in the
// past (for clock drift) and valid for 9 minutes (GitHub allows 10).
func (a *App) JWT() (string, error) {
	if a.ID == "" || a.Key == nil {
		return "", errors.New("github app: ID and Key must be set")
	}
	now := a.now()
	header := b64json(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims := b64json(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": a.ID,
	})
	signed := header + "." + claims
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.Key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func b64json(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

// token returns an installation access token, reusing one until a minute
// before it expires.
func (a *App) token(ctx context.Context, installation int64) (string, error) {
	a.mu.Lock()
	t, ok := a.tokens[installation]
	a.mu.Unlock()
	if ok && a.now().Before(t.expires.Add(-time.Minute)) {
		return t.token, nil
	}
	jwt, err := a.JWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/app/installations/" + strconv.FormatInt(installation, 10) + "/access_tokens"
	if err := a.do(ctx, http.MethodPost, path, jwt, nil, http.StatusCreated, &out); err != nil {
		return "", fmt.Errorf("installation token: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("installation token: empty token")
	}
	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = map[int64]instToken{}
	}
	a.tokens[installation] = instToken{out.Token, out.ExpiresAt}
	a.mu.Unlock()
	return out.Token, nil
}

// Comment posts a comment on a repository security advisory (a private
// vulnerability report). repo is "owner/name".
func (a *App) Comment(ctx context.Context, installation int64, repo, ghsa, body string) error {
	if !ValidRepo(repo) || !ValidGHSA(ghsa) {
		return fmt.Errorf("github: bad repo %q or advisory %q", repo, ghsa)
	}
	tok, err := a.token(ctx, installation)
	if err != nil {
		return err
	}
	path := "/repos/" + repo + "/security-advisories/" + ghsa + "/comments"
	return a.do(ctx, http.MethodPost, path, tok, map[string]string{"body": body}, http.StatusCreated, nil)
}

// do sends one API request and decodes the JSON answer into out.
func (a *App) do(ctx context.Context, method, path, bearer string, in any, want int, out any) error {
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("User-Agent", "reprobot")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		msg := strings.TrimSpace(string(b))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, msg)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return nil
}
