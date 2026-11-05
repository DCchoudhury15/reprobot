package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reproLines is a valid repro.yaml split into named lines, so a test case
// can replace or drop one of them.
var reproLines = []struct{ key, text string }{
	{"version", "version: 1"},
	{"project", "project: tinyparse"},
	{"commit", "commit: 3f2a9c1"},
	{"profile", "profile: asan"},
	{"run", "run:"},
	{"cmd", `  cmd: ["bin/tinyparse", "poc/input.bin"]`},
	{"timeout", "  timeout_seconds: 30"},
	{"repeat", "  repeat: 5"},
	{"files", "files: [input.bin]"},
	{"claim", "claim:"},
	{"sanitizer", "  sanitizer: address"},
	{"kind", "  kind: heap-buffer-overflow"},
	{"frame", "  frame: parse_header"},
}

// reproYAML returns the valid repro.yaml with some lines replaced. An empty
// replacement drops the line.
func reproYAML(over map[string]string) string {
	var b strings.Builder
	for _, l := range reproLines {
		text := l.text
		if v, ok := over[l.key]; ok {
			text = v
		}
		if text != "" {
			b.WriteString(text + "\n")
		}
	}
	return b.String()
}

var projectLines = []struct{ key, text string }{
	{"version", "version: 1"},
	{"id", "id: tinyparse"},
	{"repo", "repo: /srv/git/tinyparse.git"},
	{"image", "image: gcc:15"},
	{"profiles", "profiles:"},
	{"asan", "  asan:"},
	{"build", `    build: make CFLAGS="-fsanitize=address -g -O1"`},
	{"outputs", "    outputs: [tinyparse]"},
	{"env", "    env: {ASAN_OPTIONS: detect_leaks=0}"},
	{"limits", "limits:"},
	{"cpus", "  cpus: 2"},
	{"memory", "  memory_mb: 2048"},
	{"buildtimeout", "  build_timeout_seconds: 900"},
}

func projectYAML(over map[string]string) string {
	var b strings.Builder
	for _, l := range projectLines {
		text := l.text
		if v, ok := over[l.key]; ok {
			text = v
		}
		if text != "" {
			b.WriteString(text + "\n")
		}
	}
	return b.String()
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadReproducerValid(t *testing.T) {
	p := writeFile(t, t.TempDir(), "repro.yaml", reproYAML(nil))
	r, err := LoadReproducer(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Project != "tinyparse" || r.Commit != "3f2a9c1" || r.Profile != "asan" {
		t.Errorf("wrong fields: %+v", r)
	}
	if len(r.Run.Cmd) != 2 || r.Run.Cmd[1] != "poc/input.bin" {
		t.Errorf("wrong cmd: %q", r.Run.Cmd)
	}
	if r.Claim.Frame != "parse_header" || r.Claim.Kind != "heap-buffer-overflow" {
		t.Errorf("wrong claim: %+v", r.Claim)
	}
}

func TestLoadReproducerAccepts(t *testing.T) {
	cases := []struct {
		name string
		over map[string]string
	}{
		{"full sha", map[string]string{"commit": "commit: 3f2a9c1d4e5f60718293a4b5c6d7e8f901234567"}},
		{"tag", map[string]string{"commit": "commit: v1.2.3"}},
		{"tag with slash", map[string]string{"commit": "commit: release/2026-01"}},
		{"numeric looking sha", map[string]string{"commit": "commit: 1234567"}},
		{"max timeout", map[string]string{"timeout": "  timeout_seconds: 300"}},
		{"max repeat", map[string]string{"repeat": "  repeat: 20"}},
		{"no files", map[string]string{"files": ""}},
		{"nested file", map[string]string{"files": "files: [inputs/a.bin, b.bin]"}},
		{"dots in file name", map[string]string{"files": "files: [a..b]"}},
		{"sanitizer none without kind", map[string]string{"sanitizer": "  sanitizer: none", "kind": "", "frame": ""}},
		{"undefined", map[string]string{"sanitizer": "  sanitizer: undefined", "kind": "  kind: signed-integer-overflow"}},
		{"id charset", map[string]string{"project": "project: a0._-z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, t.TempDir(), "repro.yaml", reproYAML(tc.over))
			if _, err := LoadReproducer(p); err != nil {
				t.Fatalf("want ok, got %v", err)
			}
		})
	}
}

func TestLoadReproducerDefaults(t *testing.T) {
	p := writeFile(t, t.TempDir(), "repro.yaml", reproYAML(map[string]string{
		"timeout": "",
		"repeat":  "  repeat: 0",
	}))
	r, err := LoadReproducer(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Run.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Errorf("timeout: got %d, want %d", r.Run.TimeoutSeconds, DefaultTimeoutSeconds)
	}
	if r.Run.Repeat != DefaultRepeat {
		t.Errorf("repeat: got %d, want %d", r.Run.Repeat, DefaultRepeat)
	}
}

func TestLoadReproducerRejects(t *testing.T) {
	cases := []struct {
		name string
		over map[string]string
		want string // must appear in the error
	}{
		{"unknown top field", map[string]string{"profile": "profile: asan\nextra: 1"}, "field extra not found"},
		{"unknown run field", map[string]string{"repeat": "  repeat: 5\n  network: true"}, "field network not found"},
		{"unknown claim field", map[string]string{"frame": "  severity: high"}, "field severity not found"},
		{"version 2", map[string]string{"version": "version: 2"}, "version must be 1"},
		{"version missing", map[string]string{"version": ""}, "version must be 1"},
		{"version as string", map[string]string{"version": `version: "one"`}, "cannot unmarshal"},
		{"project upper case", map[string]string{"project": "project: TinyParse"}, "project"},
		{"project leading dash", map[string]string{"project": "project: -tiny"}, "project"},
		{"project slash", map[string]string{"project": "project: a/b"}, "project"},
		{"project too long", map[string]string{"project": "project: " + strings.Repeat("a", 65)}, "project"},
		{"project missing", map[string]string{"project": ""}, "project"},
		{"commit missing", map[string]string{"commit": ""}, "commit"},
		{"commit option", map[string]string{"commit": "commit: -rf"}, "commit"},
		{"commit range", map[string]string{"commit": "commit: v1..v2"}, "commit"},
		{"commit dotdot in path", map[string]string{"commit": "commit: refs/../x"}, "commit"},
		{"commit tilde", map[string]string{"commit": `commit: "HEAD~1"`}, "commit"},
		{"commit caret", map[string]string{"commit": `commit: "main^"`}, "commit"},
		{"commit reflog", map[string]string{"commit": `commit: "main@{1}"`}, "commit"},
		{"commit space", map[string]string{"commit": `commit: "v1 v2"`}, "commit"},
		{"commit too long", map[string]string{"commit": "commit: v" + strings.Repeat("1", 128)}, "commit"},
		{"profile upper case", map[string]string{"profile": "profile: ASAN"}, "profile"},
		{"profile path", map[string]string{"profile": "profile: ../asan"}, "profile"},
		{"profile missing", map[string]string{"profile": ""}, "profile"},
		{"cmd empty list", map[string]string{"cmd": "  cmd: []"}, "run.cmd is empty"},
		{"cmd missing", map[string]string{"cmd": ""}, "run.cmd is empty"},
		{"cmd empty element", map[string]string{"cmd": `  cmd: ["bin/tinyparse", ""]`}, "run.cmd[1] is empty"},
		{"timeout negative", map[string]string{"timeout": "  timeout_seconds: -1"}, "timeout_seconds"},
		{"timeout too big", map[string]string{"timeout": "  timeout_seconds: 301"}, "timeout_seconds"},
		{"repeat negative", map[string]string{"repeat": "  repeat: -1"}, "repeat"},
		{"repeat too big", map[string]string{"repeat": "  repeat: 21"}, "repeat"},
		{"file absolute", map[string]string{"files": "files: [/etc/passwd]"}, "is absolute"},
		{"file parent", map[string]string{"files": "files: [../secret]"}, `contains ".."`},
		{"file parent only", map[string]string{"files": "files: [..]"}, `contains ".."`},
		{"file not clean", map[string]string{"files": "files: [a/../b]"}, "not a clean path"},
		{"file dot slash", map[string]string{"files": "files: [./a]"}, "not a clean path"},
		{"file double slash", map[string]string{"files": "files: [a//b]"}, "not a clean path"},
		{"file trailing slash", map[string]string{"files": "files: [a/]"}, "not a clean path"},
		{"file dot", map[string]string{"files": "files: [.]"}, "base dir"},
		{"file empty", map[string]string{"files": `files: [""]`}, "is empty"},
		{"file twice", map[string]string{"files": "files: [a, b, a]"}, "listed twice"},
		{"sanitizer unknown", map[string]string{"sanitizer": "  sanitizer: memory"}, "claim.sanitizer"},
		{"sanitizer wrong case", map[string]string{"sanitizer": "  sanitizer: Address"}, "claim.sanitizer"},
		{"sanitizer missing", map[string]string{"sanitizer": ""}, "claim.sanitizer"},
		{"kind missing", map[string]string{"kind": ""}, "claim.kind is required"},
		{"kind missing undefined", map[string]string{"sanitizer": "  sanitizer: undefined", "kind": ""}, "claim.kind is required"},
		{"duplicate key", map[string]string{"profile": "profile: asan\nprofile: ubsan"}, "already defined"},
		{"second document", map[string]string{"frame": "  frame: parse_header\n---\nversion: 1"}, "more than one YAML document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, t.TempDir(), "repro.yaml", reproYAML(tc.over))
			r, err := LoadReproducer(p)
			if err == nil {
				t.Fatalf("want error containing %q, got ok: %+v", tc.want, r)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error does not wrap ErrInvalid: %v", err)
			}
			// The temp path holds the test name, so leave it out.
			if msg := strings.ReplaceAll(err.Error(), p, ""); !strings.Contains(msg, tc.want) {
				t.Errorf("error %q does not contain %q", msg, tc.want)
			}
		})
	}
}

func TestLoadReproducerEmptyAndMissing(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "repro.yaml", "")
	if _, err := LoadReproducer(p); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty file: got %v", err)
	}
	if _, err := LoadReproducer(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("missing file: want error")
	}
	big := writeFile(t, dir, "big.yaml", reproYAML(nil)+"# "+strings.Repeat("x", MaxSpecFileBytes)+"\n")
	if _, err := LoadReproducer(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("big file: got %v", err)
	}
}

func TestLoadProjectValid(t *testing.T) {
	p := writeFile(t, t.TempDir(), "project.yaml", projectYAML(nil))
	proj, err := LoadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	prof, ok := proj.Profiles["asan"]
	if !ok {
		t.Fatalf("profile asan missing: %+v", proj.Profiles)
	}
	if prof.Env["ASAN_OPTIONS"] != "detect_leaks=0" || prof.Outputs[0] != "tinyparse" {
		t.Errorf("wrong profile: %+v", prof)
	}
}

func TestLoadProjectDefaults(t *testing.T) {
	p := writeFile(t, t.TempDir(), "project.yaml", projectYAML(map[string]string{
		"limits": "", "cpus": "", "memory": "", "buildtimeout": "",
	}))
	proj, err := LoadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{CPUs: DefaultCPUs, MemoryMB: DefaultMemoryMB, BuildTimeoutSeconds: DefaultBuildTimeoutSeconds}
	if proj.Limits != want {
		t.Errorf("limits: got %+v, want %+v", proj.Limits, want)
	}
}

func TestLoadProjectAccepts(t *testing.T) {
	cases := []struct {
		name string
		over map[string]string
	}{
		{"max limits", map[string]string{"cpus": "  cpus: 16", "memory": "  memory_mb: 16384", "buildtimeout": "  build_timeout_seconds: 3600"}},
		{"half cpu", map[string]string{"cpus": "  cpus: 0.5"}},
		{"nested output", map[string]string{"outputs": "    outputs: [build/bin/tinyparse]"}},
		{"no env", map[string]string{"env": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, t.TempDir(), "project.yaml", projectYAML(tc.over))
			if _, err := LoadProject(p); err != nil {
				t.Fatalf("want ok, got %v", err)
			}
		})
	}
}

func TestLoadProjectRejects(t *testing.T) {
	cases := []struct {
		name string
		over map[string]string
		want string
	}{
		{"unknown field", map[string]string{"image": "image: gcc:15\nprivileged: true"}, "field privileged not found"},
		{"unknown profile field", map[string]string{"env": "    network: host"}, "field network not found"},
		{"version 0", map[string]string{"version": "version: 0"}, "version must be 1"},
		{"bad id", map[string]string{"id": "id: Tiny"}, "id"},
		{"id missing", map[string]string{"id": ""}, "id"},
		{"repo empty", map[string]string{"repo": `repo: ""`}, "repo is empty"},
		{"repo missing", map[string]string{"repo": ""}, "repo is empty"},
		{"image empty", map[string]string{"image": ""}, "image is empty"},
		{"no profiles", map[string]string{"profiles": "profiles: {}", "asan": "", "build": "", "outputs": "", "env": ""}, "no profiles"},
		{"bad profile name", map[string]string{"asan": "  ASan:"}, "profile name"},
		{"build empty", map[string]string{"build": ""}, "profiles.asan.build is empty"},
		{"outputs empty", map[string]string{"outputs": "    outputs: []"}, "profiles.asan.outputs is empty"},
		{"output absolute", map[string]string{"outputs": "    outputs: [/bin/sh]"}, "is absolute"},
		{"output parent", map[string]string{"outputs": "    outputs: [../x]"}, `contains ".."`},
		{"output not clean", map[string]string{"outputs": "    outputs: [build/./x]"}, "not a clean path"},
		{"cpus negative", map[string]string{"cpus": "  cpus: -1"}, "limits.cpus"},
		{"cpus too big", map[string]string{"cpus": "  cpus: 17"}, "limits.cpus"},
		{"memory negative", map[string]string{"memory": "  memory_mb: -1"}, "limits.memory_mb"},
		{"memory too big", map[string]string{"memory": "  memory_mb: 16385"}, "limits.memory_mb"},
		{"build timeout negative", map[string]string{"buildtimeout": "  build_timeout_seconds: -5"}, "limits.build_timeout_seconds"},
		{"build timeout too big", map[string]string{"buildtimeout": "  build_timeout_seconds: 3601"}, "limits.build_timeout_seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeFile(t, t.TempDir(), "project.yaml", projectYAML(tc.over))
			proj, err := LoadProject(p)
			if err == nil {
				t.Fatalf("want error containing %q, got ok: %+v", tc.want, proj)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error does not wrap ErrInvalid: %v", err)
			}
			// The temp path holds the test name, so leave it out.
			if msg := strings.ReplaceAll(err.Error(), p, ""); !strings.Contains(msg, tc.want) {
				t.Errorf("error %q does not contain %q", msg, tc.want)
			}
		})
	}
}

func TestCheckAgainst(t *testing.T) {
	proj := &Project{ID: "tinyparse", Profiles: map[string]Profile{"asan": {}, "ubsan": {}}}
	ok := &Reproducer{Project: "tinyparse", Profile: "ubsan"}
	if err := ok.CheckAgainst(proj); err != nil {
		t.Errorf("want ok, got %v", err)
	}
	wrongProject := &Reproducer{Project: "other", Profile: "asan"}
	if err := wrongProject.CheckAgainst(proj); err == nil || !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("wrong project: got %v", err)
	}
	wrongProfile := &Reproducer{Project: "tinyparse", Profile: "msan"}
	err := wrongProfile.CheckAgainst(proj)
	if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "asan, ubsan") {
		t.Errorf("wrong profile: got %v", err)
	}
}
