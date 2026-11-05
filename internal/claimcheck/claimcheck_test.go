package claimcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/spec"
	"github.com/DCchoudhury15/reprobot/internal/testutil"
)

// fixtureClaim is the claim from testdata/fixtures/tinyparse/repro.
var fixtureClaim = spec.Claim{
	Sanitizer: "address",
	Kind:      "heap-buffer-overflow",
	Frame:     "parse_header",
	Text: "The memcpy in parse_header in tinyparse.c copies up to 255 bytes into a\n" +
		"16 byte heap buffer.\n",
}

// asanReport is real output of the Vuln build run on the fixture input.
const asanReport = `==1==ERROR: AddressSanitizer: heap-buffer-overflow on address 0x706f85be0020 at pc 0x744f86e707e2 bp 0x7ffcefd9e710 sp 0x7ffcefd9ded0
WRITE of size 64 at 0x706f85be0020 thread T0
    #0 0x744f86e707e1 in memcpy (/usr/local/lib64/libasan.so.8+0x1177e1)
    #1 0x00000040129c in parse_header /src/tinyparse.c:21
    #2 0x0000004013f4 in parse_file /src/tinyparse.c:35
    #3 0x0000004014ed in main /src/tinyparse.c:46
    #4 0x744f86b8eca7  (/lib/x86_64-linux-gnu/libc.so.6+0x29ca7) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)
    #5 0x744f86b8ed64 in __libc_start_main (/lib/x86_64-linux-gnu/libc.so.6+0x29d64) (BuildId: 5e5b51f4c39af9ac589408c1dc432ba390a273cc)
    #6 0x000000401160 in _start (/work/bin/tinyparse+0x401160)

0x706f85be0020 is located 0 bytes after 16-byte region [0x706f85be0010,0x706f85be0020)
allocated by thread T0 here:
    #0 0x744f86e7308b in malloc (/usr/local/lib64/libasan.so.8+0x11a08b)
    #1 0x000000401274 in parse_header /src/tinyparse.c:18

SUMMARY: AddressSanitizer: heap-buffer-overflow /src/tinyparse.c:21 in parse_header
`

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.org",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// vulnLines is the number of lines of tinyparse.c at the Vuln commit.
func vulnLines(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testutil.FixtureDir(), "vuln", "tinyparse.c"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatal("fixture tinyparse.c must end with a newline")
	}
	return strings.Count(string(b), "\n")
}

func check(t *testing.T, dir, sha string, c spec.Claim) []string {
	t.Helper()
	got, err := Check(context.Background(), dir, sha, c)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return got
}

func TestFixtureClaimHasNoFindings(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	for _, sha := range []string{f.Vuln, f.Fix, f.Revert} {
		if got := check(t, f.RepoDir, sha, fixtureClaim); len(got) != 0 {
			t.Errorf("at %s: want no findings, got %q", sha[:7], got)
		}
	}
}

func TestFindings(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	short := f.Vuln[:7]
	n := vulnLines(t)
	tests := []struct {
		name  string
		claim spec.Claim
		want  []string
	}{
		{
			name:  "invented function in frame",
			claim: spec.Claim{Frame: "parse_blob"},
			want:  []string{`function "parse_blob" does not appear anywhere in the code at ` + short},
		},
		{
			name:  "invented function in backticks",
			claim: spec.Claim{Frame: "parse_header", Text: "The bug is in `parse_blob`, called from parse_file."},
			want:  []string{`function "parse_blob" does not appear anywhere in the code at ` + short},
		},
		{
			name:  "invented function called in text",
			claim: spec.Claim{Text: "parse_header() is fine, but read_chunk(buf, n) overflows."},
			want:  []string{`function "read_chunk" does not appear anywhere in the code at ` + short},
		},
		{
			name:  "invented file",
			claim: spec.Claim{Frame: "parse_header", Text: "See src/parse.c and tinyparse.c."},
			want:  []string{`file "src/parse.c" does not exist at ` + short},
		},
		{
			name:  "longer extension is not cut short",
			claim: spec.Claim{Text: "The crash is in tinyparse.cpp."},
			want:  []string{`file "tinyparse.cpp" does not exist at ` + short},
		},
		{
			name:  "other extensions are not read as a known one",
			claim: spec.Claim{Text: "See Program.cs, notes.hx and view.tsx."},
		},
		{
			name:  "line out of range",
			claim: spec.Claim{Text: "The write at tinyparse.c:400 overflows."},
			want:  []string{fmt.Sprintf("tinyparse.c has %d lines at %s, but the report cites line 400", n, short)},
		},
		{
			name:  "line one past the end",
			claim: spec.Claim{Text: fmt.Sprintf("tinyparse.c:%d:3", n+1)},
			want:  []string{fmt.Sprintf("tinyparse.c has %d lines at %s, but the report cites line %d", n, short, n+1)},
		},
		{
			name:  "last line is in range",
			claim: spec.Claim{Text: fmt.Sprintf("tinyparse.c:%d", n)},
		},
		{
			name: "absolute build path from a sanitizer trace",
			claim: spec.Claim{Text: "#0 0x4f5 in parse_header /src/tinyparse/tinyparse.c:21:2\n" +
				"#1 0x4f6 in __interceptor_memcpy ../../../../src/libsanitizer/sanitizer_common/sanitizer_common_interceptors.inc:827\n" +
				"#2 0x4f7 in malloc ../../../../src/libsanitizer/asan/asan_malloc_linux.cpp:69"},
		},
		{
			name:  "pasted ASan report",
			claim: spec.Claim{Frame: "parse_header", Text: asanReport},
		},
		{
			name:  "stoplisted libc names are ignored",
			claim: spec.Claim{Frame: "strlen", Text: "It calls strcpy(dst, src), then sprintf() and `realloc`, then snprintf(...)."},
		},
		{
			name:  "short identifiers are ignored",
			claim: spec.Claim{Frame: "fn", Text: "`ab` and xy() are fine."},
		},
		{
			name:  "duplicates give one finding",
			claim: spec.Claim{Frame: "parse_blob", Text: "parse_blob() and `parse_blob` again."},
			want:  []string{`function "parse_blob" does not appear anywhere in the code at ` + short},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(t, f.RepoDir, f.Vuln, tt.claim)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFunctionOnlyInLaterCommit(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	dir := filepath.Join(t.TempDir(), "copy")
	runGit(t, f.RepoDir, "clone", "-q", f.RepoDir, dir)
	src := filepath.Join(dir, "tinyparse.c")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte("\nint parse_footer(void)\n{\n\treturn 0;\n}\n")...)
	if err := os.WriteFile(src, b, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "commit", "-q", "-am", "add parse_footer")
	later := runGit(t, dir, "rev-parse", "HEAD")

	c := spec.Claim{Frame: "parse_footer"}
	want := []string{`function "parse_footer" does not appear anywhere in the code at ` + f.Vuln[:7]}
	if got := check(t, dir, f.Vuln, c); !reflect.DeepEqual(got, want) {
		t.Errorf("at Vuln: got %q, want %q", got, want)
	}
	if got := check(t, dir, later, c); len(got) != 0 {
		t.Errorf("at the later commit: want no findings, got %q", got)
	}
}

// newRepo makes a git repo with one commit holding files (path to content)
// and returns its dir and sha.
func newRepo(t *testing.T, files map[string]string) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	for p, content := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "files")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

func TestAmbiguousFile(t *testing.T) {
	dir, sha := newRepo(t, map[string]string{
		"lib/a/util.c": "int util_init(void);\n",
		"lib/b/util.c": "int util_init(void);\n",
	})
	short := sha[:7]

	// Two files end in util.c, so a line beyond both is still not flagged.
	if got := check(t, dir, sha, spec.Claim{Text: "util.c:900"}); len(got) != 0 {
		t.Errorf("ambiguous path: want no findings, got %q", got)
	}
	want := []string{"lib/a/util.c has 1 line at " + short + ", but the report cites line 2"}
	if got := check(t, dir, sha, spec.Claim{Text: "a/util.c:2"}); !reflect.DeepEqual(got, want) {
		t.Errorf("unique path: got %q, want %q", got, want)
	}
	want = []string{`file "c/util.c" does not exist at ` + short}
	if got := check(t, dir, sha, spec.Claim{Text: "c/util.c"}); !reflect.DeepEqual(got, want) {
		t.Errorf("missing path: got %q, want %q", got, want)
	}
}

func TestLastLineWithoutNewline(t *testing.T) {
	dir, sha := newRepo(t, map[string]string{"noeol.c": "int a;\nint b;"})
	if got := check(t, dir, sha, spec.Claim{Text: "noeol.c:2"}); len(got) != 0 {
		t.Errorf("line 2 exists: want no findings, got %q", got)
	}
	want := []string{"noeol.c has 2 lines at " + sha[:7] + ", but the report cites line 3"}
	if got := check(t, dir, sha, spec.Claim{Text: "noeol.c:3"}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBareMirror(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	bare := filepath.Join(t.TempDir(), "mirror.git")
	runGit(t, f.RepoDir, "clone", "-q", "--mirror", f.RepoDir, bare)
	if got := check(t, bare, f.Vuln, fixtureClaim); len(got) != 0 {
		t.Errorf("fixture claim: want no findings, got %q", got)
	}
	c := spec.Claim{Frame: "parse_blob", Text: "tinyparse.c:400"}
	want := []string{
		`function "parse_blob" does not appear anywhere in the code at ` + f.Vuln[:7],
		fmt.Sprintf("tinyparse.c has %d lines at %s, but the report cites line 400", vulnLines(t), f.Vuln[:7]),
	}
	if got := check(t, bare, f.Vuln, c); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestAtMostMaxRefs(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	var b strings.Builder
	for i := range MaxRefs + 10 {
		fmt.Fprintf(&b, "made_up_%03d() ", i)
	}
	got := check(t, f.RepoDir, f.Vuln, spec.Claim{Text: b.String()})
	if len(got) != MaxRefs {
		t.Fatalf("want %d findings, got %d", MaxRefs, len(got))
	}
	if !strings.Contains(got[MaxRefs-1], fmt.Sprintf("made_up_%03d", MaxRefs-1)) {
		t.Errorf("last finding should be for the 50th name, got %q", got[MaxRefs-1])
	}
}

func TestRejectsBadSha(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	for _, sha := range []string{
		"", "deadbeef", f.Vuln[:39], f.Vuln + "0",
		strings.Repeat("g", 40), "HEAD", "--output=/tmp/x" + strings.Repeat("a", 25),
	} {
		if _, err := Check(context.Background(), f.RepoDir, sha, fixtureClaim); err == nil {
			t.Errorf("sha %q: want an error", sha)
		}
	}
}

func TestMissingCommitIsAnError(t *testing.T) {
	f := testutil.NewTinyparseRepo(t)
	missing := strings.Repeat("0", 40)
	if _, err := Check(context.Background(), f.RepoDir, missing, spec.Claim{Frame: "parse_header"}); err == nil {
		t.Error("want an error for a commit that is not in the repo, not a silent finding")
	}
}

func TestExtract(t *testing.T) {
	c := spec.Claim{
		Frame: "ns::Parser::read_chunk()",
		Text: "In `decode_frame()` at src/dec/frame.cc:12:7, frame_len(x) wraps; " +
			"see ./include/dec.hpp and util.go:3. Not `h->name`, not foo (bar).",
	}
	want := []ref{
		{kind: refFunc, name: "read_chunk"},
		{kind: refFunc, name: "decode_frame"},
		{kind: refFunc, name: "frame_len"},
		{kind: refFile, name: "src/dec/frame.cc", line: 12},
		{kind: refFile, name: "./include/dec.hpp"},
		{kind: refFile, name: "util.go", line: 3},
	}
	if got := extract(c); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}
