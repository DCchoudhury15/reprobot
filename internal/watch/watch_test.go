package watch

import (
	"debug/elf"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseMaps(t *testing.T) {
	in := `55d0c0a00000-55d0c0a02000 r--p 00000000 08:01 123  /work/bin/tinyparse
7f1c2a400000-7f1c2a5f0000 r-xp 00028000 08:01 456  /usr/local/lib64/libasan.so.8.0.0
7ffd5c1e0000-7ffd5c201000 rw-p 00000000 00:00 0    [stack]
7f00 bad line
7f1c2b000000-7f1c2b001000 r-xp 00000000 00:2a 9    /tmp/a b.so (deleted)
`
	got := ParseMaps([]byte(in))
	want := []Mapping{
		{0x55d0c0a00000, 0x55d0c0a02000, 0, "r--p", "/work/bin/tinyparse"},
		{0x7f1c2a400000, 0x7f1c2a5f0000, 0x28000, "r-xp", "/usr/local/lib64/libasan.so.8.0.0"},
		{0x7ffd5c1e0000, 0x7ffd5c201000, 0, "rw-p", "[stack]"},
		{0x7f1c2b000000, 0x7f1c2b001000, 0, "r-xp", "/tmp/a b.so (deleted)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func noOpen(string) (*elf.File, error) { return nil, errors.New("not opened") }

func TestFromSanitizerLibraries(t *testing.T) {
	m := func(perms, path string) []Mapping {
		return []Mapping{{Start: 0x1000, End: 0x2000, Perms: perms, Path: path}}
	}
	tests := []struct {
		name string
		maps []Mapping
		ok   bool
		why  string
	}{
		{"gcc asan", m("r-xp", "/usr/local/lib64/libasan.so.8.0.0"), true, "sanitizer runtime /usr/local/lib64/libasan.so.8.0.0"},
		{"gcc ubsan", m("r-xp", "/usr/lib/x86_64-linux-gnu/libubsan.so.1"), true, "libubsan.so.1"},
		{"clang asan", m("r-xp", "/usr/lib/clang/18/lib/linux/libclang_rt.asan-x86_64.so"), true, "libclang_rt.asan"},
		{"libc", m("r-xp", "/usr/lib/x86_64-linux-gnu/libc.so.6"), false, "not opened"},
		{"look-alike name", m("r-xp", "/usr/lib/libasan.so.8.evil"), false, "not opened"},
		{"fake runtime in tmp", m("r-xp", "/tmp/libasan.so.8"), false, "a place the program can write to"},
		{"fake runtime in shm", m("r-xp", "/dev/shm/libasan.so.8"), false, "a place the program can write to"},
		{"fake runtime from reporter", m("r-xp", "/work/poc/libasan.so.8"), false, "a place the program can write to"},
		{"fake runtime in log dir", m("r-xp", "/work/out/tok/libasan.so.8"), false, "a place the program can write to"},
		{"deleted file", m("r-xp", "/usr/lib/libasan.so.8 (deleted)"), false, "not code from a file"},
		{"not executable", m("rw-p", "/usr/lib/libasan.so.8"), false, "not code from a file"},
		{"anonymous code", m("rwxp", ""), false, "not code from a file"},
		{"jit or stack", m("rwxp", "[stack]"), false, "not code from a file"},
		{"unmapped", []Mapping{{Start: 0x3000, End: 0x4000, Perms: "r-xp", Path: "/usr/lib/libasan.so.8"}}, false, "not mapped"},
	}
	for _, tt := range tests {
		ok, why := FromSanitizer(0x1800, tt.maps, noOpen)
		if ok != tt.ok || !strings.Contains(why, tt.why) {
			t.Errorf("%s: got %v %q, want %v and %q", tt.name, ok, why, tt.ok, tt.why)
		}
	}
}

// symbolAt must find the function around an address. Build a small Go
// program (go test strips its own binary) and map its code segment at a
// different address than its file, as a PIE would be.
func TestSymbolAt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a program")
	}
	bin := filepath.Join(t.TempDir(), "watch")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/DCchoudhury15/reprobot/cmd/reprobot-watch")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	var fn elf.Symbol
	for _, s := range syms {
		if s.Name == "main.write" {
			fn = s
		}
	}
	var text *elf.Prog
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 && p.Vaddr <= fn.Value && fn.Value < p.Vaddr+p.Filesz {
			text = p
		}
	}
	if fn.Name == "" || text == nil {
		t.Fatal("no main.write or no code segment")
	}
	const base = 0x7f0000000000
	m := Mapping{Start: base, End: base + text.Filesz, Offset: text.Off, Perms: "r-xp", Path: bin}
	addr := base + (fn.Value - text.Vaddr) + 4
	if name, err := symbolAt(&m, addr, elf.Open); err != nil || name != "main.write" {
		t.Errorf("symbolAt = %q, %v; want main.write", name, err)
	}
	if name, err := symbolAt(&m, base+text.Filesz+0x1000, elf.Open); err == nil {
		t.Errorf("an address past the segment gave %q", name)
	}
}
