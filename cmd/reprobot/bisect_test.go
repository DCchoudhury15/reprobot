package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/sign"
	"github.com/DCchoudhury15/reprobot/internal/source"
)

func fakeSHA(i int) string { return fmt.Sprintf("%040x", i+1) }

// fakeBisect is a history of 9 commits after the good one (commit 0); the
// crash starts at commit 6.
func fakeBisect() (func(context.Context, runner.Options) (*result.Result, error), runner.PathFunc) {
	crash := &result.Crash{Kind: "heap-buffer-overflow", Signature: "A"}
	run := func(_ context.Context, o runner.Options) (*result.Result, error) {
		commit := o.Commit
		if commit == "" {
			commit = fakeSHA(9)
		}
		var i int
		fmt.Sscanf(commit, "%x", &i)
		r := &result.Result{State: result.NotReproduced, Inputs: result.Inputs{Project: "p", Commit: commit}}
		if i-1 >= 6 {
			r.State, r.Crash = result.ReproducedAsClaimed, crash
		}
		return r, nil
	}
	path := func(_ context.Context, _ runner.Options, good, bad string) (string, []string, error) {
		if good != "v1.0" {
			return "", nil, fmt.Errorf("%w: %s", source.ErrUnknownRef, good)
		}
		var out []string
		for i := 1; i <= 9; i++ {
			out = append(out, fakeSHA(i))
		}
		return fakeSHA(0), out, nil
	}
	return run, path
}

func TestRunBisect(t *testing.T) {
	run, path := fakeBisect()
	var out, errOut bytes.Buffer
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--bisect", "v1.0", "r"}, &out, &errOut, run, path); code != exitAsClaimed {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"bisect:    FOUND", "first bad: " + fakeSHA(6), "last good: " + fakeSHA(5), fakeSHA(0)[:12] + "  good"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output has no %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--bisect", "v1.0", "--json", "r"}, &out, &errOut, run, path); code != exitAsClaimed {
		t.Fatalf("json exit %d", code)
	}
	var b result.Bisect
	if err := json.Unmarshal(out.Bytes(), &b); err != nil || b.Bisect != result.Found || b.FirstBad != fakeSHA(6) || b.GoodCommit != "v1.0" || b.Report == nil || b.Commits != 9 {
		t.Errorf("json %+v, %v", b, err)
	}

	// An unknown good commit is a verdict, not an infrastructure error.
	out.Reset()
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--bisect", "v9", "r"}, &out, &errOut, run, path); code != exitVerdict || !strings.Contains(out.String(), "CANNOT_TELL") {
		t.Errorf("unknown good commit: exit %d\n%s", code, out.String())
	}

	// --fix and --bisect do not go together.
	ran := false
	never := func(context.Context, runner.Options) (*result.Result, error) { ran = true; return nil, nil }
	if code := runMain(context.Background(), []string{"--project", "p", "--fix", "b", "--bisect", "v1.0", "r"}, &out, &errOut, never, path); code != exitUsage || ran {
		t.Errorf("--fix with --bisect: exit %d, ran %v", code, ran)
	}
}

func TestSignedBisectVerify(t *testing.T) {
	dir := t.TempDir()
	keygenCmd([]string{dir}, &bytes.Buffer{}, &bytes.Buffer{})
	key, pub := filepath.Join(dir, "signing.key"), filepath.Join(dir, "signing.pub")
	run, path := fakeBisect()
	var out, errOut bytes.Buffer
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--bisect", "v1.0", "--sign-key", key, "r"}, &out, &errOut, run, path); code != exitAsClaimed {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	env, err := sign.Decode(out.Bytes())
	if err != nil || env.PayloadType != sign.BisectPayloadType {
		t.Fatalf("envelope %+v, %v", env, err)
	}
	var o, e bytes.Buffer
	if code := verifyCmd([]string{"--key", pub, "-"}, bytes.NewReader(out.Bytes()), &o, &e); code != 0 {
		t.Fatalf("verify exit %d: %s", code, e.String())
	}
	if !strings.Contains(e.String(), "bisect FOUND") || !strings.Contains(e.String(), "good commit v1.0") {
		t.Errorf("summary: %s", e.String())
	}
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	forged := *env
	forged.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte(fakeSHA(6)), []byte(fakeSHA(3)), -1))
	data, _ := json.Marshal(forged)
	if code := verifyCmd([]string{"--key", pub, "-"}, bytes.NewReader(data), &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("forged first bad commit verified: exit %d", code)
	}
	// A signed bisect cannot pass as a signed result.
	if _, err := sign.Verify(env, mustPub(t, pub)); err == nil {
		t.Error("a bisect envelope verified as a result")
	}
}

func mustPub(t *testing.T, path string) ed25519.PublicKey {
	t.Helper()
	pub, err := sign.LoadPublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
