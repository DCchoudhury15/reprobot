package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
	"github.com/DCchoudhury15/reprobot/internal/sign"
)

func TestKeygenSignedRunVerify(t *testing.T) {
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	if code := keygenCmd([]string{dir}, &out, &errOut); code != 0 {
		t.Fatalf("keygen exit %d: %s", code, errOut.String())
	}
	keyID := strings.TrimSpace(out.String())
	if code := keygenCmd([]string{dir}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("second keygen into the same dir: exit %d, want 1", code)
	}
	key, pub := filepath.Join(dir, "signing.key"), filepath.Join(dir, "signing.pub")

	want := sampleResult()
	ran := false
	fake := func(context.Context, runner.Options) (*result.Result, error) { ran = true; return want, nil }
	out.Reset()
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--sign-key", key, "r"}, &out, &errOut, fake, nil); code != 0 {
		t.Fatalf("signed run exit %d: %s", code, errOut.String())
	}
	envPath := filepath.Join(dir, "result.json")
	os.WriteFile(envPath, out.Bytes(), 0o644)
	env, err := sign.Decode(out.Bytes())
	if err != nil || env.Signatures[0].KeyID != keyID {
		t.Fatalf("envelope %+v, %v; want key id %s", env, err, keyID)
	}

	verify := func(args []string, stdin string) (int, string, string) {
		var o, e bytes.Buffer
		code := verifyCmd(args, strings.NewReader(stdin), &o, &e)
		return code, o.String(), e.String()
	}
	for _, args := range [][]string{{"--key", pub, envPath}, {envPath, "--key", pub}, {"--key", pub, "-"}} {
		code, o, e := verify(args, out.String())
		var got result.Result
		if code != 0 || json.Unmarshal([]byte(o), &got) != nil || got.State != want.State || got.Inputs != want.Inputs {
			t.Errorf("verify %v: exit %d\n%s\n%s", args, code, o, e)
		}
		if !strings.Contains(e, "good signature by key "+keyID) || !strings.Contains(e, string(want.State)) {
			t.Errorf("verify %v summary: %s", args, e)
		}
	}

	// Change the verdict inside the envelope: verify must refuse it and
	// print nothing on stdout.
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	forged := *env
	forged.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload,
		[]byte(string(want.State)), []byte(string(result.NotReproduced)), 1))
	data, _ := json.Marshal(forged)
	if code, o, _ := verify([]string{"--key", pub, "-"}, string(data)); code != 1 || o != "" {
		t.Errorf("forged verdict: exit %d stdout %q", code, o)
	}
	other := t.TempDir()
	keygenCmd([]string{other}, &bytes.Buffer{}, &bytes.Buffer{})
	if code, o, _ := verify([]string{"--key", filepath.Join(other, "signing.pub"), envPath}, ""); code != 1 || o != "" {
		t.Errorf("other key: exit %d stdout %q", code, o)
	}
	if code, _, _ := verify([]string{envPath}, ""); code != 2 {
		t.Errorf("no --key: exit %d", code)
	}

	// A bad key stops the run before it starts.
	ran = false
	for _, args := range [][]string{
		{"--project", "p", "--cache", "/c", "--sign-key", pub, "r"},
		{"--project", "p", "--cache", "/c", "--sign-key", filepath.Join(dir, "missing"), "r"},
		{"--project", "p", "--cache", "/c", "--json", "--sign-key", key, "r"},
	} {
		if code := runMain(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, fake, nil); code != exitUsage || ran {
			t.Errorf("%v: exit %d ran %v, want %d and no run", args, code, ran, exitUsage)
		}
	}
}

// TestSignedFixCheckVerify signs a fix check with --fix --sign-key and checks
// it with verify. A forged fix verdict must not verify.
func TestSignedFixCheckVerify(t *testing.T) {
	dir := t.TempDir()
	keygenCmd([]string{dir}, &bytes.Buffer{}, &bytes.Buffer{})
	key, pub := filepath.Join(dir, "signing.key"), filepath.Join(dir, "signing.pub")
	crash := &result.Crash{Kind: "heap-buffer-overflow", Signature: "A"}
	fake := func(_ context.Context, o runner.Options) (*result.Result, error) {
		if o.Commit == "" {
			return &result.Result{State: result.ReproducedAsClaimed, Crash: crash, Inputs: result.Inputs{Project: "p", Commit: "aaaaaaaaaa"}}, nil
		}
		return &result.Result{State: result.NotReproduced, Inputs: result.Inputs{Project: "p", Commit: "bbbbbbbbbb"}}, nil
	}
	var out, errOut bytes.Buffer
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--fix", "bbbbbbb", "--sign-key", key, "r"}, &out, &errOut, fake, nil); code != exitAsClaimed {
		t.Fatalf("signed fix check exit %d: %s", code, errOut.String())
	}
	env, err := sign.Decode(out.Bytes())
	if err != nil || env.PayloadType != sign.FixPayloadType {
		t.Fatalf("envelope %+v, %v; want payload type %s", env, err, sign.FixPayloadType)
	}

	var o, e bytes.Buffer
	if code := verifyCmd([]string{"--key", pub, "-"}, bytes.NewReader(out.Bytes()), &o, &e); code != 0 {
		t.Fatalf("verify exit %d: %s", code, e.String())
	}
	var fc result.FixCheck
	if err := json.Unmarshal(o.Bytes(), &fc); err != nil || fc.Fix != result.Fixed || fc.FixRun == nil || fc.FixRun.Inputs.Commit != "bbbbbbbbbb" {
		t.Errorf("verified fix check %+v, %v", fc, err)
	}
	if !strings.Contains(e.String(), "fix check FIXED") || !strings.Contains(e.String(), "fix commit bbbbbbb") {
		t.Errorf("verify summary: %s", e.String())
	}

	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	forged := *env
	forged.Payload = base64.StdEncoding.EncodeToString(bytes.Replace(payload, []byte(`"FIXED"`), []byte(`"NOT_FIXED"`), 1))
	data, _ := json.Marshal(forged)
	o.Reset()
	if code := verifyCmd([]string{"--key", pub, "-"}, bytes.NewReader(data), &o, &bytes.Buffer{}); code != 1 || o.Len() != 0 {
		t.Errorf("forged fix verdict: exit %d stdout %q", code, o.String())
	}
}
