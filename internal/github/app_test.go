package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		if testKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
			panic(err)
		}
	})
	return testKey
}

func TestParseKey(t *testing.T) {
	k := key(t)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	b8, _ := x509.MarshalPKCS8PrivateKey(k)
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b8})
	for name, p := range map[string][]byte{"pkcs1": pkcs1, "pkcs8": pkcs8} {
		got, err := ParseKey(p)
		if err != nil || !got.Equal(k) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ParseKey([]byte("not a key")); err == nil {
		t.Error("garbage parsed")
	}
}

// The JWT must verify with the App's public key and carry the claims GitHub
// asks for: iat 60s in the past, exp at most 10 minutes ahead, iss the id.
func TestJWT(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := &App{ID: "Iv23liABC", Key: key(t), Now: func() time.Time { return now }}
	tok, err := a.JWT()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %q", tok)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key(t).PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("signature: %v", err)
	}
	var head map[string]string
	var claims map[string]any
	decode := func(s string, v any) {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatal(err)
		}
	}
	decode(parts[0], &head)
	decode(parts[1], &claims)
	if head["alg"] != "RS256" {
		t.Errorf("header = %v", head)
	}
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if iat != now.Unix()-60 || exp <= now.Unix() || exp > now.Unix()+600 || claims["iss"] != "Iv23liABC" {
		t.Errorf("claims = %v", claims)
	}
	if _, err := (&App{Key: key(t)}).JWT(); err == nil {
		t.Error("JWT without an ID")
	}
}

// fakeGitHub answers the token and comment endpoints and records requests.
type fakeGitHub struct {
	mu       sync.Mutex
	tokens   int
	comments []string // "path body"
	auth     []string
	status   int // comment answer; 0 means 201
	expires  time.Time
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("X-GitHub-Api-Version"))
	switch {
	case r.Method == "POST" && r.URL.Path == "/app/installations/52219614/access_tokens":
		f.tokens++
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test" + string(rune('0'+f.tokens)), "expires_at": f.expires})
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments"):
		var in struct{ Body string }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &in)
		f.comments = append(f.comments, r.URL.Path+" "+in.Body)
		if f.status != 0 {
			http.Error(w, `{"message":"Not Found"}`, f.status)
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1}`))
	default:
		http.NotFound(w, r)
	}
}

func TestComment(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	f := &fakeGitHub{expires: now.Add(time.Hour)}
	srv := httptest.NewServer(f)
	defer srv.Close()
	a := &App{ID: "123", Key: key(t), BaseURL: srv.URL, Now: func() time.Time { return now }}
	ctx := context.Background()
	for _, body := range []string{"first", "second"} {
		if err := a.Comment(ctx, 52219614, "octo-org/tinyparse", "GHSA-8f4m-hccc-8qph", body); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokens != 1 {
		t.Errorf("%d token requests, want 1 (the token is reused)", f.tokens)
	}
	want := []string{
		"/repos/octo-org/tinyparse/security-advisories/GHSA-8f4m-hccc-8qph/comments first",
		"/repos/octo-org/tinyparse/security-advisories/GHSA-8f4m-hccc-8qph/comments second",
	}
	if strings.Join(f.comments, "\n") != strings.Join(want, "\n") {
		t.Errorf("comments = %q", f.comments)
	}
	if !strings.HasPrefix(f.auth[0], "POST /app/installations/52219614/access_tokens Bearer ey") ||
		f.auth[1] != "POST /repos/octo-org/tinyparse/security-advisories/GHSA-8f4m-hccc-8qph/comments Bearer ghs_test1 2022-11-28" {
		t.Errorf("auth = %q", f.auth)
	}

	// A token close to expiry is replaced.
	now = now.Add(59*time.Minute + time.Second)
	if err := a.Comment(ctx, 52219614, "octo-org/tinyparse", "GHSA-8f4m-hccc-8qph", "third"); err != nil {
		t.Fatal(err)
	}
	if f.tokens != 2 {
		t.Errorf("%d token requests, want 2", f.tokens)
	}

	f.status = http.StatusNotFound
	err := a.Comment(ctx, 52219614, "octo-org/tinyparse", "GHSA-8f4m-hccc-8qph", "x")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
	n := len(f.comments)
	if err := a.Comment(ctx, 52219614, "octo-org/../x", "GHSA-8f4m-hccc-8qph", "x"); err == nil || len(f.comments) != n {
		t.Errorf("bad repo was sent: %v", err)
	}
}
