// Package watch checks a sanitizer crash from outside the crashing process.
//
// The runner starts reprobot-watch as PID 1 of the run container. It starts
// the reporter's command as a ptrace child and stops it when it exits. A
// sanitizer ends a process by calling exit_group with its exitcode from
// inside its own runtime (__sanitizer::internal__exit). The watcher reads
// the registers at that moment and checks that the exit_group call came
// from sanitizer runtime code, not from libc or an interpreter. A program
// that read the secret log dir and exit code from its environment, wrote a
// fake report and called exit() with the code fails this check.
//
// The verdict cannot be forged by the program: the watcher writes it only
// after every other process in the container is dead, and PID 1 of a pid
// namespace cannot be killed from inside it.
package watch

import (
	"bufio"
	"bytes"
	"debug/elf"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// FileName is the verdict file the watcher writes into the secret log dir.
const FileName = "watch.json"

// Verdict is what the watcher saw.
type Verdict struct {
	// Pid is the program's pid inside the container. Sanitizer log files
	// are named <log_path>.<pid>.
	Pid int `json:"pid"`
	// Status is the program's exit code, or 128 + signal.
	Status int `json:"status"`
	// SanitizerExit is true when the program ended with exit_group, with
	// the sanitizer exit code, called from sanitizer runtime code.
	SanitizerExit bool `json:"sanitizer_exit"`
	// Why explains SanitizerExit.
	Why string `json:"why,omitempty"`
	// Error is set when the watcher itself failed (for example, ptrace is
	// not allowed). The run then says nothing about the program.
	Error string `json:"error,omitempty"`
}

// Mapping is one line of /proc/<pid>/maps.
type Mapping struct {
	Start, End, Offset uint64
	Perms              string
	Path               string
}

// ParseMaps parses /proc/<pid>/maps.
func ParseMaps(b []byte) []Mapping {
	var maps []Mapping
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		// start-end perms offset dev inode [path]
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		lo, hi, ok := strings.Cut(f[0], "-")
		if !ok {
			continue
		}
		start, err1 := strconv.ParseUint(lo, 16, 64)
		end, err2 := strconv.ParseUint(hi, 16, 64)
		off, err3 := strconv.ParseUint(f[2], 16, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		m := Mapping{Start: start, End: end, Offset: off, Perms: f[1]}
		if len(f) >= 6 {
			m.Path = strings.Join(f[5:], " ")
		}
		maps = append(maps, m)
	}
	return maps
}

// runtimeLib matches the file names of shared sanitizer runtimes (GCC and
// clang). Distributions strip these, so their symbols cannot be checked.
var runtimeLib = regexp.MustCompile(`^(lib(asan|ubsan|hwasan|tsan|msan|lsan)\.so(\.[0-9]+)*|libclang_rt\.(asan|ubsan_standalone|ubsan_minimal|hwasan|tsan|msan|lsan)(-[a-z0-9_]+)?\.so)$`)

// writable are the places in a run container that the program can write
// to, or that hold reporter files. Code mapped from there is not trusted.
var writable = []string{"/tmp/", "/var/tmp/", "/dev/", "/run/", "/proc/", "/work/out/", "/work/poc/"}

// runtimeNamespace is the mangled prefix of functions in the sanitizer
// runtime's internal namespace, where internal__exit lives.
const runtimeNamespace = "_ZN11__sanitizer"

// FromSanitizer says whether the instruction at addr is sanitizer runtime
// code: either in a shared sanitizer runtime library, or inside a function
// of the __sanitizer namespace in the program (a static runtime, as clang
// links it). open opens a mapped file for its symbols.
func FromSanitizer(addr uint64, maps []Mapping, open func(path string) (*elf.File, error)) (bool, string) {
	var m *Mapping
	for i := range maps {
		if maps[i].Start <= addr && addr < maps[i].End {
			m = &maps[i]
			break
		}
	}
	if m == nil {
		return false, fmt.Sprintf("exit_group was called from %#x, which is not mapped", addr)
	}
	if !strings.Contains(m.Perms, "x") || !strings.HasPrefix(m.Path, "/") || strings.HasSuffix(m.Path, " (deleted)") {
		return false, fmt.Sprintf("exit_group was called from %#x, which is not code from a file (%q)", addr, m.Path)
	}
	for _, dir := range writable {
		if strings.HasPrefix(m.Path, dir) {
			return false, fmt.Sprintf("exit_group was called from code in %s, a place the program can write to", m.Path)
		}
	}
	if runtimeLib.MatchString(path.Base(m.Path)) {
		return true, "exit_group was called from the sanitizer runtime " + m.Path
	}
	name, err := symbolAt(m, addr, open)
	if err != nil {
		return false, fmt.Sprintf("exit_group was called from %s, which is not a sanitizer runtime (%v)", m.Path, err)
	}
	if strings.HasPrefix(name, runtimeNamespace) {
		return true, fmt.Sprintf("exit_group was called from the sanitizer runtime function %s in %s", name, m.Path)
	}
	return false, fmt.Sprintf("exit_group was called from %s in %s, not from the sanitizer runtime", name, m.Path)
}

// symbolAt names the function at addr in the file mapped by m.
func symbolAt(m *Mapping, addr uint64, open func(string) (*elf.File, error)) (string, error) {
	f, err := open(m.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	off := addr - m.Start + m.Offset
	vaddr, ok := uint64(0), false
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Off <= off && off < p.Off+p.Filesz {
			vaddr, ok = off-p.Off+p.Vaddr, true
			break
		}
	}
	if !ok {
		return "", fmt.Errorf("offset %#x is in no loaded segment", off)
	}
	var syms []elf.Symbol
	if s, err := f.Symbols(); err == nil {
		syms = append(syms, s...)
	}
	if s, err := f.DynamicSymbols(); err == nil {
		syms = append(syms, s...)
	}
	for _, s := range syms {
		if elf.ST_TYPE(s.Info) == elf.STT_FUNC && s.Value <= vaddr && vaddr < s.Value+s.Size {
			return s.Name, nil
		}
	}
	return "", fmt.Errorf("no function symbol at %#x", vaddr)
}
