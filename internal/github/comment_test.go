package github

import (
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

func TestCommentBody(t *testing.T) {
	res := &result.Result{
		State: result.ReproducedAsClaimed, Reason: "crashed in 3 of 3 runs, as claimed",
		Inputs:  result.Inputs{Project: "tinyparse", Commit: "0123456789abcdef0123", Profile: "asan"},
		Runs:    make([]result.RunOutcome, 3),
		Crashed: 3,
		Crash: &result.Crash{Sanitizer: "address", Kind: "heap-buffer-overflow", Signature: "abcdef0123456789",
			Frames: []result.Frame{{Function: "__interceptor_memcpy", Module: "/usr/lib/libasan.so.8"}, {Function: "parse_header", File: "/src/tinyparse.c", Line: 21}}},
		ClaimFindings: []string{"function \"@octocat`\n**pwn**\" does not appear anywhere in the code"},
	}
	fix := &result.FixCheck{FixCommit: "v1.1", Fix: result.Fixed, Reason: "crashed before, no crash after"}
	got := CommentBody(res, fix, "https://reprobot.example.org/reports/1")
	for _, want := range []string{
		"**reprobot: REPRODUCED_AS_CLAIMED**",
		"`crashed in 3 of 3 runs, as claimed`",
		"- Tested: `tinyparse` at `0123456789ab`, profile `asan`",
		"- Crash: `address` `heap-buffer-overflow` in `parse_header`, signature `abcdef012345`",
		"- Runs: 3 of 3 crashed",
		"- Fix check on `v1.1`: **FIXED**",
		"Full result: https://reprobot.example.org/reports/1",
		// The reporter's text stays in one code span: no mention, no
		// Markdown, no line break.
		"- `function \"@octocat' **pwn**\" does not appear anywhere in the code`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "—") {
		t.Error("em dash in comment")
	}
	// No result link without a public URL, and no fix line without a check.
	got = CommentBody(&result.Result{State: result.NotReproduced, Reason: "no crash in 3 runs"}, nil, "")
	if strings.Contains(got, "Full result") || strings.Contains(got, "Fix check") || strings.Contains(got, "Crash:") {
		t.Errorf("got:\n%s", got)
	}
}

func TestRejectBody(t *testing.T) {
	got := RejectBody("file \"`x`\" bad\n@team")
	if !strings.Contains(got, "`file \"'x'\" bad @team`") {
		t.Errorf("got:\n%s", got)
	}
}
