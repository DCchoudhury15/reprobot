package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/store"
)

// finishOwned is finish for a report submitted by the token owner.
func finishOwned(t *testing.T, st *store.Store, project, owner string, res *result.Result) string {
	t.Helper()
	ctx := context.Background()
	id, _, _, err := st.CreateReport(ctx, store.ReportInput{Project: project, Owner: owner, Dir: "/data/reports/x"})
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

func (e *env) getAs(t *testing.T, token, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", e.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestTokensReadOnlyTheirReports(t *testing.T) {
	tokens, secret := tokensFile(t, "alice", "bob", "tp-maint tinyparse", "other-maint other,another", "admin *")
	e := newEnvTokens(t, tokens)
	crash := &result.Result{State: result.ReproducedAsClaimed,
		Crash: &result.Crash{Kind: "heap-buffer-overflow", Signature: "sig", Site: "site"}}

	a1 := finishOwned(t, e.st, "tinyparse", "alice", crash)
	a2 := finishOwned(t, e.st, "tinyparse", "alice", crash)
	b := finishOwned(t, e.st, "tinyparse", "bob", crash)
	anon := finishOwned(t, e.st, "tinyparse", "", crash)
	o := finishOwned(t, e.st, "other", "bob", crash)

	// Which reports each token reads; every other one must be a 404 that
	// looks the same as an id that does not exist.
	reads := map[string][]string{
		"alice":       {a1, a2},
		"bob":         {b, o},
		"tp-maint":    {a1, a2, b, anon},
		"other-maint": {o},
		"admin":       {a1, a2, b, anon, o},
	}
	all := []string{a1, a2, b, anon, o}
	for who, ids := range reads {
		for _, id := range all {
			want := http.StatusNotFound
			if slices.Contains(ids, id) {
				want = http.StatusOK
			}
			for _, p := range []string{"/v1/reports/" + id, "/reports/" + id} {
				if code, out := e.getAs(t, secret[who], p); code != want {
					t.Errorf("%s GET %s = %d, want %d: %s", who, p, code, want, out)
				}
			}
		}
	}

	// Duplicate lists hold only reports the reader may open.
	dups := func(who, id string) string {
		t.Helper()
		code, out := e.getAs(t, secret[who], "/v1/reports/"+id)
		if code != http.StatusOK {
			t.Fatalf("%s GET %s = %d %s", who, id, code, out)
		}
		return fmt.Sprint(decode[struct {
			Duplicates []string `json:"duplicates"`
		}](t, out).Duplicates)
	}
	for _, c := range []struct{ who, id, want string }{
		{"alice", a1, fmt.Sprint([]string{a2})},
		{"bob", b, "[]"},
		{"tp-maint", a1, fmt.Sprint([]string{a2, b, anon})},
		{"admin", b, fmt.Sprint([]string{a1, a2, anon})},
	} {
		if got := dups(c.who, c.id); got != c.want {
			t.Errorf("%s sees duplicates of %s = %s, want %s", c.who, c.id, got, c.want)
		}
	}
}

func TestIdempotencyKeyIsPerToken(t *testing.T) {
	tokens, secret := tokensFile(t, "alice", "bob")
	e := newEnvTokens(t, tokens)
	post := func(who string) (int, createResp) {
		t.Helper()
		body, ctype := form(t, goodRepro, nil, nil)
		req, _ := http.NewRequest("POST", e.ts.URL+"/v1/reports", body)
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Idempotency-Key", "same-key")
		req.Header.Set("Authorization", "Bearer "+secret[who])
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, decode[createResp](t, out)
	}
	codeA, a := post("alice")
	codeB, b := post("bob")
	codeA2, a2 := post("alice")
	codeB2, b2 := post("bob")
	if codeA != http.StatusAccepted || codeB != http.StatusAccepted || codeA2 != http.StatusOK || codeB2 != http.StatusOK {
		t.Errorf("codes alice %d, bob %d, alice again %d, bob again %d; want 202 202 200 200", codeA, codeB, codeA2, codeB2)
	}
	if b2 != b {
		t.Errorf("bob's retry = %+v, want %+v", b2, b)
	}
	if b.ReportID == a.ReportID {
		t.Errorf("bob's key returned alice's report %s", a.ReportID)
	}
	if a2 != a {
		t.Errorf("alice's retry = %+v, want %+v", a2, a)
	}
	if code, _ := e.getAs(t, secret["bob"], a.StatusURL); code != http.StatusNotFound {
		t.Errorf("bob reads alice's report: %d", code)
	}
	if code, _ := e.getAs(t, secret["alice"], a.StatusURL); code != http.StatusOK {
		t.Errorf("alice reads her own report: %d", code)
	}
}
