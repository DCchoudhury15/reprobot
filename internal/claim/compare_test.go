package claim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sanitizer"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// crash parses real captured output from testdata/sanitizer/out.
func crash(t *testing.T, name string) *result.Crash {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sanitizer", "out", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	c := sanitizer.Parse(string(b))
	if c == nil {
		t.Fatalf("%s: no crash parsed", name)
	}
	return c
}

func TestCompare(t *testing.T) {
	asan := func(kind, frame string) spec.Claim {
		return spec.Claim{Sanitizer: "address", Kind: kind, Frame: frame}
	}
	cases := []struct {
		name     string
		out      string // file in testdata/sanitizer/out, "" for no crash
		claim    spec.Claim
		match    bool
		findings []string
	}{
		{"exact claim", "tinyparse", asan("heap-buffer-overflow", "parse_header"), true, nil},
		{"no frame claimed", "tinyparse", asan("heap-buffer-overflow", ""), true, nil},
		{"second frame", "tinyparse", asan("heap-buffer-overflow", "parse_file"), true, nil},
		{"frame with parens", "tinyparse", asan("heap-buffer-overflow", "parse_header()"), true, nil},
		{"kind with spaces and case", "tinyparse", asan("Heap Buffer Overflow", "parse_header"), true, nil},
		{"kind with underscores", "tinyparse", asan("heap_buffer_overflow", ""), true, nil},
		{"wrong kind", "tinyparse", asan("stack-buffer-overflow", "parse_header"), false,
			[]string{`claimed kind "stack-buffer-overflow" but saw "heap-buffer-overflow"`}},
		{"wrong frame", "tinyparse", asan("heap-buffer-overflow", "parse_name"), false,
			[]string{`claimed frame "parse_name" is not in the top 5 project frames: parse_header, parse_file, main`}},
		{"runtime frame is not a project frame", "tinyparse", asan("heap-buffer-overflow", "memcpy"), false,
			[]string{`claimed frame "memcpy" is not in the top 5 project frames: parse_header, parse_file, main`}},
		{"wrong sanitizer", "tinyparse", spec.Claim{Sanitizer: "undefined", Kind: "heap-buffer-overflow"}, false,
			[]string{`claimed sanitizer "undefined" but saw "address"`}},
		{"everything wrong", "tinyparse", spec.Claim{Sanitizer: "undefined", Kind: "signed-integer-overflow", Frame: "add"}, false,
			[]string{
				`claimed sanitizer "undefined" but saw "address"`,
				`claimed kind "signed-integer-overflow" but saw "heap-buffer-overflow"`,
				`claimed frame "add" is not in the top 5 project frames: parse_header, parse_file, main`,
			}},
		{"no crash", "", asan("heap-buffer-overflow", "parse_header"), false,
			[]string{"no crash was observed"}},
		{"different bug same kind", "heap_overflow_write", asan("heap-buffer-overflow", "parse_header"), false,
			[]string{`claimed frame "parse_header" is not in the top 5 project frames: make_label, print_label, main`}},
		{"fifth frame counts", "deep_stack", asan("heap-buffer-overflow", "step3"), true, nil},
		{"sixth frame does not", "deep_stack", asan("heap-buffer-overflow", "step2"), false,
			[]string{`claimed frame "step2" is not in the top 5 project frames: step7, step6, step5, step4, step3`}},
		{"c++ short name", "cpp_overflow", asan("heap-buffer-overflow", "store::Buffer::at"), true, nil},
		{"c++ full name", "cpp_overflow", asan("heap-buffer-overflow", "store::Buffer::at(unsigned long) const"), true, nil},
		{"c++ unqualified name", "cpp_overflow", asan("heap-buffer-overflow", "Buffer::at"), false,
			[]string{`claimed frame "Buffer::at" is not in the top 5 project frames: store::Buffer::at, store::last_value, main`}},
		{"segv case", "null_deref", asan("segv", "second_value"), true, nil},
		{"null deref claimed as heap bug", "null_deref", asan("heap-use-after-free", ""), false,
			[]string{`claimed kind "heap-use-after-free" but saw "SEGV"`}},
		{"double free", "double_free", asan("double-free", "release"), true, nil},
		{"use after free", "use_after_free", asan("heap-use-after-free", "session_id"), true, nil},
		{"ubsan with stack", "ubsan_overflow_stack",
			spec.Claim{Sanitizer: "undefined", Kind: "signed-integer-overflow", Frame: "add_score"}, true, nil},
		{"ubsan without stack, no frame claimed", "ubsan_overflow",
			spec.Claim{Sanitizer: "undefined", Kind: "signed-integer-overflow"}, true, nil},
		{"ubsan without stack, frame claimed", "ubsan_overflow",
			spec.Claim{Sanitizer: "undefined", Kind: "signed-integer-overflow", Frame: "add_score"}, false,
			[]string{`claimed frame "add_score" but the crash has no project frames`}},
		{"ubsan wrong kind", "ubsan_shift",
			spec.Claim{Sanitizer: "undefined", Kind: "signed-integer-overflow"}, false,
			[]string{`claimed kind "signed-integer-overflow" but saw "shift-out-of-bounds"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c *result.Crash
			if tc.out != "" {
				c = crash(t, tc.out)
			}
			match, findings := Compare(tc.claim, c)
			if match != tc.match {
				t.Errorf("match: got %v, want %v (findings %q)", match, tc.match, findings)
			}
			if strings.Join(findings, "\n") != strings.Join(tc.findings, "\n") {
				t.Errorf("findings:\n got %q\nwant %q", findings, tc.findings)
			}
		})
	}
}

func TestNormalizeKind(t *testing.T) {
	cases := map[string]string{
		"heap-buffer-overflow":    "heap-buffer-overflow",
		"Heap Buffer Overflow":    "heap-buffer-overflow",
		"HEAP_BUFFER_OVERFLOW":    "heap-buffer-overflow",
		" use after free ":        "use-after-free",
		"SEGV":                    "segv",
		"signed integer_overflow": "signed-integer-overflow",
	}
	for in, want := range cases {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}
