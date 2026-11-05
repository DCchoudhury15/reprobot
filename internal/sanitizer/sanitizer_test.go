package sanitizer

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
)

// The files in testdata/sanitizer/out are real gcc 15 output captured by
// testdata/sanitizer/regen.sh.
func readOut(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sanitizer", "out", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustParse(t *testing.T, name string) *result.Crash {
	t.Helper()
	c := Parse(readOut(t, name))
	if c == nil {
		t.Fatalf("%s: no crash parsed", name)
	}
	return c
}

func names(frames []result.Frame) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.Function
	}
	return out
}

func TestParseOutputs(t *testing.T) {
	cases := []struct {
		name      string
		sanitizer string
		kind      string
		access    string
		size      int
		frames    int // frames in the crash stack only
		project   []string
		sigText   string
	}{
		{"heap_overflow_read", "address", "heap-buffer-overflow", "READ", 1, 6,
			[]string{"sum_bytes", "checksum", "main"},
			"heap-buffer-overflow|sum_bytes|checksum|main"},
		{"heap_overflow_read_O0", "address", "heap-buffer-overflow", "READ", 1, 6,
			[]string{"sum_bytes", "checksum", "main"},
			"heap-buffer-overflow|sum_bytes|checksum|main"},
		{"heap_overflow_read_shifted", "address", "heap-buffer-overflow", "READ", 1, 6,
			[]string{"sum_bytes", "checksum", "main"},
			"heap-buffer-overflow|sum_bytes|checksum|main"},
		{"heap_overflow_write", "address", "heap-buffer-overflow", "WRITE", 25, 7,
			[]string{"make_label", "print_label", "main"},
			"heap-buffer-overflow|make_label|print_label|main"},
		{"stack_overflow", "address", "stack-buffer-overflow", "WRITE", 4, 6,
			[]string{"fill", "sum_window", "main"},
			"stack-buffer-overflow|fill|sum_window|main"},
		{"global_overflow", "address", "global-buffer-overflow", "READ", 4, 5,
			[]string{"lookup_limit", "main"},
			"global-buffer-overflow|lookup_limit|main"},
		{"use_after_free", "address", "heap-use-after-free", "READ", 4, 5,
			[]string{"session_id", "main"},
			"heap-use-after-free|session_id|main"},
		{"double_free", "address", "double-free", "", 0, 6,
			[]string{"release", "main"},
			"double-free|release|main"},
		{"null_deref", "address", "SEGV", "READ", 0, 5,
			[]string{"second_value", "main"},
			"SEGV|second_value|main"},
		{"cpp_overflow", "address", "heap-buffer-overflow", "READ", 4, 6,
			[]string{"store::Buffer::at(unsigned long) const",
				"store::last_value(store::Buffer const&, unsigned long)", "main"},
			"heap-buffer-overflow|store::Buffer::at|store::last_value|main"},
		{"deep_stack", "address", "heap-buffer-overflow", "READ", 4, 11,
			[]string{"step7", "step6", "step5", "step4", "step3", "step2", "step1", "main"},
			"heap-buffer-overflow|step7|step6|step5"},
		{"bad_free", "address", "bad-free", "", 0, 6,
			[]string{"drop_tail", "main"},
			"bad-free|drop_tail|main"},
		// The header says "requested allocation size ..."; the kind comes
		// from the SUMMARY line.
		{"alloc_too_big", "address", "allocation-size-too-big", "", 0, 4,
			[]string{"make_table", "main"},
			"allocation-size-too-big|make_table|main"},
		{"ubsan_overflow", "undefined", "signed-integer-overflow", "", 0, 1,
			nil,
			"signed-integer-overflow|ubsan_overflow.c"},
		{"ubsan_overflow_stack", "undefined", "signed-integer-overflow", "", 0, 6,
			[]string{"add_score", "total_score", "main"},
			"signed-integer-overflow|add_score|total_score|main"},
		{"ubsan_shift", "undefined", "shift-out-of-bounds", "", 0, 5,
			[]string{"shift_left", "main"},
			"shift-out-of-bounds|shift_left|main"},
		{"ubsan_null", "undefined", "null-pointer-dereference", "", 0, 5,
			[]string{"load_int", "main"},
			"null-pointer-dereference|load_int|main"},
		{"ubsan_member", "undefined", "null-pointer-dereference", "", 0, 5,
			[]string{"point_x", "main"},
			"null-pointer-dereference|point_x|main"},
		{"ubsan_index", "undefined", "index-out-of-bounds", "", 0, 5,
			[]string{"pick", "main"},
			"index-out-of-bounds|pick|main"},
		{"ubsan_misaligned", "undefined", "misaligned-address", "", 0, 5,
			[]string{"read_misaligned", "main"},
			"misaligned-address|read_misaligned|main"},
		{"ubsan_div", "undefined", "division-by-zero", "", 0, 5,
			[]string{"divide", "main"},
			"division-by-zero|divide|main"},
		{"tinyparse", "address", "heap-buffer-overflow", "WRITE", 64, 7,
			[]string{"parse_header", "parse_file", "main"},
			"heap-buffer-overflow|parse_header|parse_file|main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := mustParse(t, tc.name)
			if c.Sanitizer != tc.sanitizer || c.Kind != tc.kind {
				t.Errorf("got %s/%s, want %s/%s", c.Sanitizer, c.Kind, tc.sanitizer, tc.kind)
			}
			if c.Access != tc.access || c.Size != tc.size {
				t.Errorf("access: got %q size %d, want %q size %d", c.Access, c.Size, tc.access, tc.size)
			}
			if len(c.Frames) != tc.frames {
				t.Errorf("got %d frames, want %d: %+v", len(c.Frames), tc.frames, c.Frames)
			}
			for i, f := range c.Frames {
				if f.Index != i {
					t.Errorf("frame %d has index %d", i, f.Index)
				}
			}
			got := names(ProjectFrames(c))
			if strings.Join(got, "\n") != strings.Join(tc.project, "\n") {
				t.Errorf("project frames: got %q, want %q", got, tc.project)
			}
			if c.SignatureText != tc.sigText {
				t.Errorf("signature text: got %q, want %q", c.SignatureText, tc.sigText)
			}
			sum := sha256.Sum256([]byte(tc.sigText))
			if want := hex.EncodeToString(sum[:])[:16]; c.Signature != want {
				t.Errorf("signature: got %q, want %q", c.Signature, want)
			}
		})
	}
}

func TestParseFrameFields(t *testing.T) {
	c := mustParse(t, "tinyparse")
	want := []result.Frame{
		{Index: 0, Function: "memcpy", Module: "/usr/local/lib64/libasan.so.8"},
		{Index: 1, Function: "parse_header", File: "/fixture/vuln/tinyparse.c", Line: 21},
		{Index: 2, Function: "parse_file", File: "/fixture/vuln/tinyparse.c", Line: 35},
		{Index: 3, Function: "main", File: "/fixture/vuln/tinyparse.c", Line: 46},
		{Index: 4, Module: "/lib/x86_64-linux-gnu/libc.so.6"},
		{Index: 5, Function: "__libc_start_main", Module: "/lib/x86_64-linux-gnu/libc.so.6"},
		{Index: 6, Function: "_start", Module: "/tmp/prog"},
	}
	if len(c.Frames) != len(want) {
		t.Fatalf("got %d frames: %+v", len(c.Frames), c.Frames)
	}
	for i := range want {
		if c.Frames[i] != want[i] {
			t.Errorf("frame %d: got %+v, want %+v", i, c.Frames[i], want[i])
		}
	}

	u := mustParse(t, "ubsan_overflow")
	only := result.Frame{Index: 0, File: "/src/ubsan_overflow.c", Line: 7}
	if len(u.Frames) != 1 || u.Frames[0] != only {
		t.Errorf("ubsan without stack: got %+v, want [%+v]", u.Frames, only)
	}
}

func TestSignatureStable(t *testing.T) {
	base := mustParse(t, "heap_overflow_read")
	shifted := mustParse(t, "heap_overflow_read_shifted")
	o0 := mustParse(t, "heap_overflow_read_O0")

	// Check that the test data really differs where it should.
	if base.Frames[0].Line == shifted.Frames[0].Line {
		t.Fatalf("shifted output has the same line numbers: %d", base.Frames[0].Line)
	}
	if readOut(t, "heap_overflow_read") == readOut(t, "heap_overflow_read_O0") {
		t.Fatal("-O0 and -O1 outputs are identical")
	}

	if shifted.Signature != base.Signature {
		t.Errorf("moved lines changed the signature: %q vs %q", shifted.SignatureText, base.SignatureText)
	}
	if o0.Signature != base.Signature {
		t.Errorf("-O0 vs -O1 changed the signature: %q vs %q", o0.SignatureText, base.SignatureText)
	}
}

func TestSignatureDiffers(t *testing.T) {
	// One output per distinct bug. Same-bug variants are left out.
	bugs := []string{
		"heap_overflow_read", "heap_overflow_write", "stack_overflow",
		"global_overflow", "use_after_free", "double_free", "null_deref",
		"cpp_overflow", "deep_stack", "bad_free", "alloc_too_big", "ubsan_overflow", "ubsan_overflow_stack", "ubsan_shift",
		"ubsan_null", "ubsan_member", "ubsan_index", "ubsan_misaligned",
		"ubsan_div", "tinyparse",
	}
	seen := map[string]string{}
	for _, name := range bugs {
		c := mustParse(t, name)
		if other, ok := seen[c.Signature]; ok {
			t.Errorf("%s and %s share signature %s (%q)", name, other, c.Signature, c.SignatureText)
		}
		seen[c.Signature] = name
	}
}

func TestParseNoCrash(t *testing.T) {
	if c := Parse(readOut(t, "no_bug")); c != nil {
		t.Errorf("no_bug: got %+v", c)
	}
	if c := Parse(""); c != nil {
		t.Errorf("empty: got %+v", c)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		b := make([]byte, 1+rng.Intn(4096))
		for j := range b {
			b[j] = byte(rng.Intn(256))
			if rng.Intn(40) == 0 {
				b[j] = '\n'
			}
		}
		if c := Parse(string(b)); c != nil {
			t.Fatalf("random input %d parsed as a crash: %+v", i, c)
		}
	}
}

// TestHelperGoPanic is not a real test. TestParseIgnoresGoPanic runs it in
// a child process to get real Go panic output.
func TestHelperGoPanic(t *testing.T) {
	if os.Getenv("SANITIZER_HELPER_PANIC") != "1" {
		t.Skip("helper")
	}
	var s []int
	i := len(os.Args) + 5
	_ = s[i]
}

func TestParseIgnoresGoPanic(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperGoPanic$")
	cmd.Env = append(os.Environ(), "SANITIZER_HELPER_PANIC=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	_ = cmd.Run()
	out := stderr.String()
	if !strings.Contains(out, "panic: runtime error: index out of range") {
		t.Fatalf("helper did not panic as expected:\n%s", out)
	}
	if c := Parse(out); c != nil {
		t.Errorf("Go panic parsed as a sanitizer crash: %+v", c)
	}
}

func TestParseKindFromHeaderWithoutSummary(t *testing.T) {
	// Cut the SUMMARY line, as in a truncated log. The kind must then come
	// from the "attempting ..." header.
	cases := map[string]string{
		"double_free":    "double-free",
		"bad_free":       "bad-free",
		"tinyparse":      "heap-buffer-overflow",
		"use_after_free": "heap-use-after-free",
	}
	for name, want := range cases {
		var kept []string
		for _, l := range strings.Split(readOut(t, name), "\n") {
			if !strings.HasPrefix(l, "SUMMARY:") {
				kept = append(kept, l)
			}
		}
		c := Parse(strings.Join(kept, "\n"))
		if c == nil || c.Kind != want {
			t.Errorf("%s: got %+v, want kind %s", name, c, want)
		}
	}
}

func TestParseFirstReportWins(t *testing.T) {
	ubsan := readOut(t, "ubsan_overflow")
	asan := readOut(t, "tinyparse")

	c := Parse(ubsan + asan)
	if c == nil || c.Sanitizer != "undefined" {
		t.Fatalf("ubsan first: got %+v", c)
	}
	// UBSan printed no stack, so the ASan stack below must not be used.
	if len(c.Frames) != 1 || c.Frames[0].Function != "" {
		t.Errorf("ubsan first: took frames from the next report: %+v", c.Frames)
	}

	c = Parse(asan + ubsan)
	if c == nil || c.Sanitizer != "address" || c.Kind != "heap-buffer-overflow" {
		t.Fatalf("asan first: got %+v", c)
	}

	// A first report without SUMMARY must not take the kind from the
	// SUMMARY of the next report.
	var kept []string
	for _, l := range strings.Split(readOut(t, "double_free"), "\n") {
		if !strings.HasPrefix(l, "SUMMARY:") {
			kept = append(kept, l)
		}
	}
	c = Parse(strings.Join(kept, "\n") + readOut(t, "alloc_too_big"))
	if c == nil || c.Kind != "double-free" {
		t.Fatalf("two asan reports: got %+v, want kind double-free", c)
	}
}

func TestParseWindowsLineEndings(t *testing.T) {
	out := strings.ReplaceAll(readOut(t, "tinyparse"), "\n", "\r\n")
	c := Parse(out)
	if c == nil || c.SignatureText != "heap-buffer-overflow|parse_header|parse_file|main" || c.Size != 64 {
		t.Fatalf("got %+v", c)
	}
}

func TestProjectFramesNil(t *testing.T) {
	if f := ProjectFrames(nil); f != nil {
		t.Errorf("got %+v", f)
	}
}

func TestTrimArgs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"parse_header", "parse_header"},
		{"store::Buffer::at(unsigned long) const", "store::Buffer::at"},
		{"store::last_value(store::Buffer const&, unsigned long)", "store::last_value"},
		{"operator new[](unsigned long)", "operator new[]"},
		{"Foo::operator()(int) const", "Foo::operator()"},
		{"Foo::operator()", "Foo::operator()"},
		{"(anonymous namespace)::helper(char const*)", "(anonymous namespace)::helper"},
		{"std::function<void (int)>::operator()(int) const", "std::function<void (int)>::operator()"},
		{"Obj::get() &&", "Obj::get"},
		{"parse_header()", "parse_header"},
		{"  main ", "main"},
	}
	for _, tc := range cases {
		if got := TrimArgs(tc.in); got != tc.want {
			t.Errorf("TrimArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
