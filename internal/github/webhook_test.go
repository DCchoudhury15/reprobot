package github

import (
	"os"
	"strings"
	"testing"
)

// The example from GitHub's "Validating webhook deliveries" page.
func TestVerifySignatureGitHubExample(t *testing.T) {
	secret := []byte("It's a Secret to Everybody")
	body := []byte("Hello, World!")
	want := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := Sign(secret, body); got != want {
		t.Errorf("Sign = %s, want %s", got, want)
	}
	if !VerifySignature(secret, body, want) {
		t.Error("the documented signature does not verify")
	}
	for name, h := range map[string]string{
		"empty":         "",
		"sha1 header":   "sha1=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
		"no prefix":     "757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
		"one bit off":   "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e16",
		"not hex":       "sha256=zz",
		"truncated":     "sha256=757107ea",
		"uppercase key": "SHA256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17",
	} {
		if VerifySignature(secret, body, h) {
			t.Errorf("%s: %q verified", name, h)
		}
	}
	if VerifySignature(secret, []byte("Hello, World?"), want) {
		t.Error("a changed body verified")
	}
	if VerifySignature(nil, body, Sign(nil, body)) {
		t.Error("an empty secret verified")
	}
}

func TestParseAdvisoryEvent(t *testing.T) {
	body, err := os.ReadFile("testdata/advisory_reported.json")
	if err != nil {
		t.Fatal(err)
	}
	ev, err := ParseAdvisoryEvent(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Action != "reported" || ev.Advisory.GHSAID != "GHSA-8f4m-hccc-8qph" || ev.Repository.FullName != "octo-org/tinyparse" ||
		ev.Installation.ID != 52219614 || ev.Sender.Login != "mona" || ev.Advisory.State != "triage" {
		t.Errorf("event = %+v", ev)
	}
	if !strings.Contains(ev.Advisory.Description, "```reprobot\n") {
		t.Errorf("description = %q", ev.Advisory.Description)
	}

	bad := func(old, new string) {
		t.Helper()
		b := strings.Replace(string(body), old, new, 1)
		if b == string(body) {
			t.Fatalf("%q not in payload", old)
		}
		if _, err := ParseAdvisoryEvent([]byte(b)); err == nil {
			t.Errorf("%q -> %q: want an error", old, new)
		}
	}
	bad(`"ghsa_id": "GHSA-8f4m-hccc-8qph"`, `"ghsa_id": "GHSA-8f4m-hccc-8qph/../../x"`)
	bad(`"full_name": "octo-org/tinyparse"`, `"full_name": "octo-org/../tinyparse"`)
	bad(`"full_name": "octo-org/tinyparse"`, `"full_name": "octo-org/.."`)
	bad(`"id": 52219614`, `"id": 0`)

	// Other actions are returned without checks; the caller ignores them.
	pub := strings.Replace(string(body), `"action": "reported"`, `"action": "published"`, 1)
	if ev, err := ParseAdvisoryEvent([]byte(pub)); err != nil || ev.Action != "published" {
		t.Errorf("published: %+v, %v", ev, err)
	}
}

func TestValidRepoAndGHSA(t *testing.T) {
	for s, want := range map[string]bool{
		"octo-org/tinyparse": true, "a/b.c_d-e": true, "a/.github": true,
		"a/b/c": false, "a": false, "/b": false, "a/": false, "a/..": false, "a/.": false,
		"a b/c": false, "a/b?x=1": false, "a/b#x": false, "a/b%2f": false,
	} {
		if ValidRepo(s) != want {
			t.Errorf("ValidRepo(%q) = %v", s, !want)
		}
	}
	for s, want := range map[string]bool{
		"GHSA-8f4m-hccc-8qph": true, "GHSA-xxxx-xxxx-xxxx": true,
		"GHSA-8f4m-hccc-8qp": false, "ghsa-8f4m-hccc-8qph": false, "GHSA-8f4m-hccc-8qpa": false, "GHSA-8f4m-hccc-8qph/x": false,
	} {
		if ValidGHSA(s) != want {
			t.Errorf("ValidGHSA(%q) = %v", s, !want)
		}
	}
}
