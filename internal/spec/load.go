package spec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotImplemented marks a stub that another part of the work will fill in.
var ErrNotImplemented = errors.New("not implemented")

// ErrInvalid is wrapped by every error that comes from bad input: a field
// that breaks a rule, an unknown field, a reporter file that is missing,
// too big or outside the repro dir. Callers map it to INVALID_INPUT.
var ErrInvalid = errors.New("invalid input")

// Defaults and hard maximums for maintainer limits.
const (
	DefaultCPUs                = 2
	MaxCPUs                    = 16
	DefaultMemoryMB            = 2048
	MaxMemoryMB                = 16384
	DefaultBuildTimeoutSeconds = 900
	MaxBuildTimeoutSeconds     = 3600
)

// Size limits for input files.
const (
	MaxSpecFileBytes  = 1 << 20  // repro.yaml or project config
	MaxFileBytes      = 10 << 20 // one reporter file
	MaxTotalFileBytes = 50 << 20 // all reporter files together
)

var (
	idRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	tagRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// invalid collects rule violations for one file.
type invalid struct {
	file     string
	problems []string
}

func (v *invalid) add(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

func (v *invalid) err() error {
	if len(v.problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalid, v.file, strings.Join(v.problems, "; "))
}

// LoadReproducer reads a repro.yaml file, rejects unknown fields, validates
// it and fills in defaults (timeout, repeat).
func LoadReproducer(path string) (*Reproducer, error) {
	var r Reproducer
	if err := decodeFile(path, &r); err != nil {
		return nil, err
	}
	v := &invalid{file: path}
	r.validate(v)
	if err := v.err(); err != nil {
		return nil, err
	}
	return &r, nil
}

// LoadProject reads a project config file, rejects unknown fields and
// validates it.
func LoadProject(path string) (*Project, error) {
	var p Project
	if err := decodeFile(path, &p); err != nil {
		return nil, err
	}
	v := &invalid{file: path}
	p.validate(v)
	if err := v.err(); err != nil {
		return nil, err
	}
	return &p, nil
}

// decodeFile strictly decodes exactly one YAML document into out.
func decodeFile(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxSpecFileBytes+1))
	if err != nil {
		return err
	}
	if len(b) > MaxSpecFileBytes {
		return fmt.Errorf("%w: %s: file is larger than %d bytes", ErrInvalid, path, MaxSpecFileBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: %s: file is empty", ErrInvalid, path)
		}
		return fmt.Errorf("%w: %s: %v", ErrInvalid, path, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s: more than one YAML document", ErrInvalid, path)
	}
	return nil
}

func (r *Reproducer) validate(v *invalid) {
	if r.Version != 1 {
		v.add("version must be 1, got %d", r.Version)
	}
	if !idRe.MatchString(r.Project) {
		v.add("project %q is not a valid id", r.Project)
	}
	if !validCommit(r.Commit) {
		v.add("commit %q is not a commit hash or tag name", r.Commit)
	}
	if !idRe.MatchString(r.Profile) {
		v.add("profile %q is not a valid profile name", r.Profile)
	}

	if len(r.Run.Cmd) == 0 {
		v.add("run.cmd is empty")
	}
	for i, a := range r.Run.Cmd {
		if a == "" {
			v.add("run.cmd[%d] is empty", i)
		}
	}
	switch {
	case r.Run.TimeoutSeconds < 0:
		v.add("run.timeout_seconds must not be negative")
	case r.Run.TimeoutSeconds > MaxTimeoutSeconds:
		v.add("run.timeout_seconds %d is above the maximum %d", r.Run.TimeoutSeconds, MaxTimeoutSeconds)
	case r.Run.TimeoutSeconds == 0:
		r.Run.TimeoutSeconds = DefaultTimeoutSeconds
	}
	switch {
	case r.Run.Repeat < 0:
		v.add("run.repeat must not be negative")
	case r.Run.Repeat > MaxRepeat:
		v.add("run.repeat %d is above the maximum %d", r.Run.Repeat, MaxRepeat)
	case r.Run.Repeat == 0:
		r.Run.Repeat = DefaultRepeat
	}

	seen := map[string]bool{}
	for _, f := range r.Files {
		if msg := checkRelPath(f); msg != "" {
			v.add("files: %q %s", f, msg)
			continue
		}
		if seen[f] {
			v.add("files: %q is listed twice", f)
		}
		seen[f] = true
	}

	switch r.Claim.Sanitizer {
	case "address", "undefined":
		if r.Claim.Kind == "" {
			v.add("claim.kind is required when claim.sanitizer is %q", r.Claim.Sanitizer)
		}
	case "none":
	default:
		v.add("claim.sanitizer %q must be one of address, undefined, none", r.Claim.Sanitizer)
	}
}

func (p *Project) validate(v *invalid) {
	if p.Version != 1 {
		v.add("version must be 1, got %d", p.Version)
	}
	if !idRe.MatchString(p.ID) {
		v.add("id %q is not a valid id", p.ID)
	}
	if strings.TrimSpace(p.Repo) == "" {
		v.add("repo is empty")
	}
	if strings.TrimSpace(p.Image) == "" {
		v.add("image is empty")
	}
	if len(p.Profiles) == 0 {
		v.add("no profiles defined")
	}
	names := make([]string, 0, len(p.Profiles))
	for name := range p.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prof := p.Profiles[name]
		if !idRe.MatchString(name) {
			v.add("profile name %q is not valid", name)
		}
		if strings.TrimSpace(prof.Build) == "" {
			v.add("profiles.%s.build is empty", name)
		}
		if len(prof.Outputs) == 0 {
			v.add("profiles.%s.outputs is empty", name)
		}
		for _, o := range prof.Outputs {
			if msg := checkRelPath(o); msg != "" {
				v.add("profiles.%s.outputs: %q %s", name, o, msg)
			}
		}
	}

	switch {
	case p.Limits.CPUs < 0:
		v.add("limits.cpus must not be negative")
	case p.Limits.CPUs > MaxCPUs:
		v.add("limits.cpus %g is above the maximum %d", p.Limits.CPUs, MaxCPUs)
	case p.Limits.CPUs == 0:
		p.Limits.CPUs = DefaultCPUs
	}
	switch {
	case p.Limits.MemoryMB < 0:
		v.add("limits.memory_mb must not be negative")
	case p.Limits.MemoryMB > MaxMemoryMB:
		v.add("limits.memory_mb %d is above the maximum %d", p.Limits.MemoryMB, MaxMemoryMB)
	case p.Limits.MemoryMB == 0:
		p.Limits.MemoryMB = DefaultMemoryMB
	}
	switch {
	case p.Limits.BuildTimeoutSeconds < 0:
		v.add("limits.build_timeout_seconds must not be negative")
	case p.Limits.BuildTimeoutSeconds > MaxBuildTimeoutSeconds:
		v.add("limits.build_timeout_seconds %d is above the maximum %d", p.Limits.BuildTimeoutSeconds, MaxBuildTimeoutSeconds)
	case p.Limits.BuildTimeoutSeconds == 0:
		p.Limits.BuildTimeoutSeconds = DefaultBuildTimeoutSeconds
	}
}

// validCommit accepts a 7 to 40 char hex hash or a tag name. Tag names
// start with a letter or digit, so they can never be read as a git option.
func validCommit(s string) bool {
	if shaRe.MatchString(s) {
		return true
	}
	return tagRe.MatchString(s) && !strings.Contains(s, "..")
}

// checkRelPath returns why p is not a clean relative path inside its base
// dir, or "" when it is fine.
func checkRelPath(p string) string {
	switch {
	case p == "":
		return "is empty"
	case filepath.IsAbs(p):
		return "is absolute"
	case filepath.Clean(p) != p:
		return "is not a clean path"
	case p == ".":
		return "is the base dir itself"
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return `contains ".."`
		}
	}
	return ""
}

// CheckAgainst checks that the reproducer fits the project: same project id
// and a profile name the project defines.
func (r *Reproducer) CheckAgainst(p *Project) error {
	if r.Project != p.ID {
		return fmt.Errorf("%w: reproducer is for project %q, not %q", ErrInvalid, r.Project, p.ID)
	}
	if _, ok := p.Profiles[r.Profile]; !ok {
		names := make([]string, 0, len(p.Profiles))
		for name := range p.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Errorf("%w: project %q has no profile %q (known: %s)",
			ErrInvalid, p.ID, r.Profile, strings.Join(names, ", "))
	}
	return nil
}

// FilePaths returns absolute paths of the reporter files. Every file must
// stay inside baseDir: no absolute paths, no "..", no symlink that leaves
// baseDir.
//
// Each path has its symlinks resolved, and paths[i] belongs to r.Files[i].
// Files must be regular files, at most MaxFileBytes each and
// MaxTotalFileBytes together.
func (r *Reproducer) FilePaths(baseDir string) ([]string, error) {
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return nil, err
	}
	var total int64
	paths := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		if msg := checkRelPath(f); msg != "" {
			return nil, fmt.Errorf("%w: file %q %s", ErrInvalid, f, msg)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(base, f))
		if err != nil {
			return nil, fmt.Errorf("%w: file %q: %v", ErrInvalid, f, err)
		}
		rel, err := filepath.Rel(base, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("%w: file %q points outside the repro dir", ErrInvalid, f)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: file %q: %v", ErrInvalid, f, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: file %q is not a regular file", ErrInvalid, f)
		}
		if info.Size() > MaxFileBytes {
			return nil, fmt.Errorf("%w: file %q is larger than %d bytes", ErrInvalid, f, MaxFileBytes)
		}
		total += info.Size()
		if total > MaxTotalFileBytes {
			return nil, fmt.Errorf("%w: files are larger than %d bytes in total", ErrInvalid, MaxTotalFileBytes)
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

// ReproducerHash returns a sha256 hex over the repro.yaml bytes and the
// reporter files, so the same input always gives the same hash.
//
// Encoding: a version tag, then the repro.yaml bytes, the number of files,
// and for each file in name order its name and contents. Every variable
// length part is prefixed with its length as a big endian uint64, so moving
// bytes from one part to the next always changes the hash.
func ReproducerHash(reproPath string, r *Reproducer) (string, error) {
	repro, err := os.ReadFile(reproPath)
	if err != nil {
		return "", err
	}
	paths, err := r.FilePaths(filepath.Dir(reproPath))
	if err != nil {
		return "", err
	}
	order := make([]int, len(r.Files))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return r.Files[order[a]] < r.Files[order[b]] })

	h := sha256.New()
	io.WriteString(h, "reprobot-reproducer-v1\n")
	writePart(h, repro)
	writeLen(h, uint64(len(r.Files)))
	for _, i := range order {
		data, err := os.ReadFile(paths[i])
		if err != nil {
			return "", err
		}
		if len(data) > MaxFileBytes {
			return "", fmt.Errorf("%w: file %q is larger than %d bytes", ErrInvalid, r.Files[i], MaxFileBytes)
		}
		writePart(h, []byte(r.Files[i]))
		writePart(h, data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeLen(h hash.Hash, n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	h.Write(b[:])
}

func writePart(h hash.Hash, b []byte) {
	writeLen(h, uint64(len(b)))
	h.Write(b)
}
