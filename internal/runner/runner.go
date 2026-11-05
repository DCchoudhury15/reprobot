// Package runner checks one reproducer end to end.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/DCchoudhury15/reprobot/internal/build"
	"github.com/DCchoudhury15/reprobot/internal/claim"
	"github.com/DCchoudhury15/reprobot/internal/claimcheck"
	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/sandbox"
	"github.com/DCchoudhury15/reprobot/internal/sanitizer"
	"github.com/DCchoudhury15/reprobot/internal/source"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Options for one check.
type Options struct {
	ProjectPath string // maintainer project config
	ReproPath   string // reporter repro.yaml; its files sit next to it
	CacheDir    string // git mirrors and build cache
}

// Settings for every proof of concept run.
const (
	RunUser         = "65534:65534" // nobody
	RunWorkDir      = "/work"
	RunPidsLimit    = 256
	RunTmpfsMB      = 64
	DefaultCPUs     = 2
	DefaultMemoryMB = 2048
	StderrTailBytes = 8 << 10
	ReportTailBytes = 16 << 10
)

// The sanitizer report channel.
//
// The verdict never comes from stdout or stderr: the reporter controls the
// input, and a program that echoes its input would let them print a fake
// sanitizer report. Instead every run gets a fresh out dir that the run
// user cannot list, with a subdir whose random name is given only to the
// sanitizer through log_path, and a random sanitizer exitcode. A crash
// counts only when a report file exists in that subdir and the exit code
// matches.
const (
	RunOutDir        = RunWorkDir + "/out"
	MaxReportBytes   = 1 << 20
	minSanitizerExit = 64
	sanitizerExits   = 57 // exit codes 64 to 120
)

// deps are the calls Run makes into other packages. Tests swap them for fakes.
type deps struct {
	loadReproducer func(path string) (*spec.Reproducer, error)
	loadProject    func(path string) (*spec.Project, error)
	checkAgainst   func(r *spec.Reproducer, p *spec.Project) error
	filePaths      func(r *spec.Reproducer, baseDir string) ([]string, error)
	reproducerHash func(reproPath string, r *spec.Reproducer) (string, error)
	resolve        func(ctx context.Context, cacheDir, repo, ref string) (string, string, error)
	claimCheck     func(ctx context.Context, mirrorDir, sha string, c spec.Claim) ([]string, error)
	build          func(ctx context.Context, cacheDir string, p *spec.Project, profile, sha, mirrorDir string) (build.Output, error)
	runtime        func(ctx context.Context) (string, error)
	sandboxRun     func(ctx context.Context, s sandbox.Spec) (sandbox.Outcome, error)
	parse          func(stderr string) *result.Crash
	compare        func(c spec.Claim, crash *result.Crash) (bool, []string)
	secret         func() (token string, exitCode int, err error)
}

func realDeps() deps {
	return deps{
		loadReproducer: spec.LoadReproducer,
		loadProject:    spec.LoadProject,
		checkAgainst:   (*spec.Reproducer).CheckAgainst,
		filePaths:      (*spec.Reproducer).FilePaths,
		reproducerHash: spec.ReproducerHash,
		resolve:        source.Resolve,
		claimCheck:     claimcheck.Check,
		build: func(ctx context.Context, cacheDir string, p *spec.Project, profile, sha, mirrorDir string) (build.Output, error) {
			b := build.Builder{CacheDir: cacheDir}
			return b.Build(ctx, p, profile, sha, mirrorDir)
		},
		runtime:    sandbox.Runtime,
		sandboxRun: sandbox.Run,
		parse:      sanitizer.Parse,
		compare:    claim.Compare,
		secret:     newSecret,
	}
}

// Run checks one reproducer and returns the verdict. Bad input gives a
// Result with State INVALID_INPUT, not an error. Errors are only for
// infrastructure problems (Docker down, disk full).
func Run(ctx context.Context, o Options) (*result.Result, error) {
	return run(ctx, o, realDeps())
}

func run(ctx context.Context, o Options, d deps) (*result.Result, error) {
	res := &result.Result{StartedAt: time.Now().UTC()}
	done := func(state result.State, reason string) (*result.Result, error) {
		res.State = state
		res.Reason = reason
		res.FinishedAt = time.Now().UTC()
		return res, nil
	}
	if o.CacheDir == "" {
		return nil, errors.New("runner: no cache dir")
	}
	cacheDir, err := filepath.Abs(o.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("runner: %w", err)
	}

	// 1. Load and check the inputs.
	in, err := loadInputs(o, d)
	if in.project != nil {
		res.Inputs.Project = in.project.ID
	}
	if in.repro != nil {
		res.Inputs.Profile = in.repro.Profile
	}
	res.Inputs.ReproducerHash = in.hash
	if err != nil {
		return done(result.InvalidInput, err.Error())
	}
	repro, project := in.repro, in.project

	// 2. A claim that is not a crash cannot be tested by running it.
	if repro.Claim.Sanitizer == "none" {
		return done(result.OutOfScope, "the claim is not a crash (claim.sanitizer is none), so running it cannot test it")
	}

	// 3. Fetch the source and pin the commit.
	mirror, sha, err := d.resolve(ctx, cacheDir, project.Repo, repro.Commit)
	if errors.Is(err, source.ErrUnknownRef) {
		return done(result.InvalidInput, fmt.Sprintf("commit %q does not exist in the project repo", repro.Commit))
	}
	if err != nil {
		return nil, fmt.Errorf("runner: fetch source: %w", err)
	}
	res.Inputs.Commit = sha

	// 4. Cheap static check of the claim text. Findings never change the
	// state by themselves.
	findings, err := d.claimCheck(ctx, mirror, sha, repro.Claim)
	if err != nil {
		return nil, fmt.Errorf("runner: claim check: %w", err)
	}
	res.ClaimFindings = findings

	// 5. Build, or reuse a cached build.
	out, err := d.build(ctx, cacheDir, project, repro.Profile, sha, mirror)
	res.Inputs.ImageDigest = out.ImageDigest
	res.Inputs.BuildCacheKey = out.Key
	res.Inputs.SandboxRuntime = out.Runtime
	res.BuildCacheHit = out.CacheHit
	if errors.Is(err, build.ErrBuildFailed) {
		res.BuildLogTail = out.LogTail
		return done(result.BuildFailed, fmt.Sprintf("profile %q did not build at %s", repro.Profile, short(sha)))
	}
	if err != nil {
		return nil, fmt.Errorf("runner: build: %w", err)
	}

	// 6. Run the proof of concept repeat times, each in a fresh sandbox.
	rt, err := d.runtime(ctx)
	if err != nil {
		return nil, fmt.Errorf("runner: sandbox runtime: %w", err)
	}
	res.Inputs.SandboxRuntime = rt
	pocDir, err := makePocDir(cacheDir, repro.Files, in.files)
	if pocDir != "" {
		defer os.RemoveAll(pocDir)
	}
	if err != nil {
		return nil, fmt.Errorf("runner: copy reporter files: %w", err)
	}
	s := runSpec(project, repro, out.BinDir, pocDir)
	for range repeat(repro.Run.Repeat) {
		ro, err := runOnce(ctx, d, cacheDir, s)
		if err != nil {
			return nil, err
		}
		res.Runs = append(res.Runs, ro)
	}

	// 7. Decide.
	state, crash, reason, cmp := Decide(repro.Claim, res.Runs, d.compare)
	res.Crash = crash
	res.ClaimFindings = append(res.ClaimFindings, cmp...)
	for _, r := range res.Runs {
		if r.Crash != nil {
			res.Crashed++
		}
	}
	return done(state, reason)
}

type inputs struct {
	repro   *spec.Reproducer
	project *spec.Project
	files   []string // absolute paths of the reporter files
	hash    string
}

// loadInputs reads and checks both files. Every error it returns is bad input.
func loadInputs(o Options, d deps) (inputs, error) {
	var in inputs
	var err error
	if in.repro, err = d.loadReproducer(o.ReproPath); err != nil {
		return in, err
	}
	if in.project, err = d.loadProject(o.ProjectPath); err != nil {
		return in, err
	}
	if err = d.checkAgainst(in.repro, in.project); err != nil {
		return in, err
	}
	profile, ok := in.project.Profiles[in.repro.Profile]
	if !ok {
		return in, fmt.Errorf("project %q has no profile %q", in.project.ID, in.repro.Profile)
	}
	if err = checkCmd(in.repro.Run.Cmd, profile.Outputs); err != nil {
		return in, err
	}
	if in.files, err = d.filePaths(in.repro, filepath.Dir(o.ReproPath)); err != nil {
		return in, err
	}
	if len(in.files) != len(in.repro.Files) {
		return in, fmt.Errorf("got %d file paths for %d files", len(in.files), len(in.repro.Files))
	}
	for i, p := range in.files {
		st, err := os.Stat(p)
		if err != nil {
			return in, fmt.Errorf("file %q: %w", in.repro.Files[i], err)
		}
		if !st.Mode().IsRegular() {
			return in, fmt.Errorf("file %q is not a regular file", in.repro.Files[i])
		}
	}
	if in.hash, err = d.reproducerHash(o.ReproPath, in.repro); err != nil {
		return in, err
	}
	return in, nil
}

// runSpec is the sandbox spec shared by every run.
func runSpec(p *spec.Project, r *spec.Reproducer, binDir, pocDir string) sandbox.Spec {
	env := map[string]string{}
	for k, v := range p.Profiles[r.Profile].Env {
		env[k] = v
	}
	if _, ok := env["UBSAN_OPTIONS"]; !ok {
		env["UBSAN_OPTIONS"] = "print_stacktrace=1"
	}
	cpus := p.Limits.CPUs
	if cpus <= 0 {
		cpus = DefaultCPUs
	}
	mem := p.Limits.MemoryMB
	if mem <= 0 {
		mem = DefaultMemoryMB
	}
	return sandbox.Spec{
		Image:   p.Image,
		Cmd:     r.Run.Cmd,
		User:    RunUser,
		Env:     env,
		WorkDir: RunWorkDir,
		Mounts: []sandbox.Mount{
			{Source: binDir, Target: RunWorkDir + "/bin", ReadOnly: true},
			{Source: pocDir, Target: RunWorkDir + "/poc", ReadOnly: true},
		},
		Network:      false,
		CPUs:         cpus,
		MemoryMB:     mem,
		PidsLimit:    RunPidsLimit,
		Timeout:      time.Duration(clamp(r.Run.TimeoutSeconds, spec.DefaultTimeoutSeconds, spec.MaxTimeoutSeconds)) * time.Second,
		ReadOnlyRoot: true,
		TmpfsMB:      RunTmpfsMB,
	}
}

func repeat(n int) int { return clamp(n, spec.DefaultRepeat, spec.MaxRepeat) }

// clamp gives def for values <= 0 and caps the rest at max. The loader
// already validates these; this is a second guard on reporter values.
func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	return min(v, max)
}

// makePocDir copies the reporter files into a new dir under
// cacheDir/tmp, keeping their relative names. Dirs and files are world
// readable because runs use an unprivileged uid.
func makePocDir(cacheDir string, names, paths []string) (string, error) {
	root := filepath.Join(cacheDir, "tmp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "poc-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return dir, err
	}
	for i, name := range names {
		rel := filepath.Clean(name)
		if !filepath.IsLocal(rel) {
			return dir, fmt.Errorf("file name %q leaves the poc dir", name)
		}
		dst := filepath.Join(dir, rel)
		if err := mkdirs(dir, filepath.Dir(rel)); err != nil {
			return dir, err
		}
		if err := copyFile(paths[i], dst); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

// mkdirs creates each dir of rel under base with mode 0755.
func mkdirs(base, rel string) error {
	if rel == "." {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if err := os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := os.Chmod(cur, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}

// tail returns the last n bytes of s, starting on a UTF-8 rune boundary.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for i := 0; i < len(s) && i < utf8.UTFMax; i++ {
		if utf8.RuneStart(s[i]) {
			return s[i:]
		}
	}
	if len(s) < utf8.UTFMax {
		return "" // only the end of a cut rune is left
	}
	return s // not UTF-8 text; keep the bytes
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// nRuns returns "1 run" or "n runs".
func nRuns(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// Decide turns run outcomes into a verdict. It is a pure function.
//
// All runs crashed: the most common crash signature is compared with the
// claim. Some runs crashed: FLAKY. No run crashed: TIMEOUT if any run hit
// the time limit, else OOM if any run hit the memory limit, else
// NOT_REPRODUCED.
func Decide(c spec.Claim, runs []result.RunOutcome, compare func(spec.Claim, *result.Crash) (bool, []string)) (state result.State, crash *result.Crash, reason string, findings []string) {
	n := len(runs)
	if n == 0 {
		return result.NotReproduced, nil, "the proof of concept was not run (0 runs)", nil
	}
	crashed, timedOut, oom := 0, 0, 0
	for _, r := range runs {
		switch {
		case r.Crash != nil:
			crashed++
		case r.TimedOut:
			timedOut++
		case r.OOMKilled:
			oom++
		}
	}
	if crashed == 0 {
		switch {
		case timedOut > 0:
			return result.Timeout, nil, fmt.Sprintf("%d of %s hit the time limit", timedOut, nRuns(n)), nil
		case oom > 0:
			return result.OOM, nil, fmt.Sprintf("%d of %s hit the memory limit", oom, nRuns(n)), nil
		default:
			return result.NotReproduced, nil, "no crash in " + nRuns(n), nil
		}
	}

	crash, count, kinds := mostCommon(runs)
	match := false
	if compare != nil {
		match, findings = compare(c, crash)
	}
	reason = fmt.Sprintf("crashed in %d of %s", crashed, nRuns(n))
	switch {
	case crashed < n:
		state = result.Flaky
	case match:
		state, reason = result.ReproducedAsClaimed, reason+", as claimed"
	default:
		state, reason = result.ReproducedDifferent, reason+", but not as claimed"
	}
	if kinds > 1 {
		reason += fmt.Sprintf("; %d different crash signatures, the most common one was seen in %s", kinds, nRuns(count))
	}
	return state, crash, reason, findings
}

// mostCommon returns the first crash with the most common signature, how
// many runs had it, and how many different signatures there were. Ties go
// to the signature seen first.
func mostCommon(runs []result.RunOutcome) (crash *result.Crash, count, kinds int) {
	counts := map[string]int{}
	var order []*result.Crash
	for _, r := range runs {
		if r.Crash == nil {
			continue
		}
		if counts[r.Crash.Signature] == 0 {
			order = append(order, r.Crash)
		}
		counts[r.Crash.Signature]++
	}
	for _, c := range order {
		if counts[c.Signature] > count {
			crash, count = c, counts[c.Signature]
		}
	}
	return crash, count, len(order)
}

// checkCmd allows only programs the maintainers build. A shell, or any
// other program from the image, would let the reporter print anything.
func checkCmd(cmd, outputs []string) error {
	var allowed []string
	for _, o := range outputs {
		p := "bin/" + filepath.Base(o)
		allowed = append(allowed, p)
		if len(cmd) > 0 && (cmd[0] == p || cmd[0] == "./"+p || cmd[0] == RunWorkDir+"/"+p) {
			return nil
		}
	}
	got := ""
	if len(cmd) > 0 {
		got = cmd[0]
	}
	return fmt.Errorf("run.cmd[0] must be a program the project builds (%s), got %q", strings.Join(allowed, " or "), got)
}

// runOnce runs the proof of concept once, with a fresh report channel.
func runOnce(ctx context.Context, d deps, cacheDir string, s sandbox.Spec) (result.RunOutcome, error) {
	token, code, err := d.secret()
	if err != nil {
		return result.RunOutcome{}, fmt.Errorf("runner: secret: %w", err)
	}
	outDir, err := makeOutDir(cacheDir, token)
	if outDir != "" {
		defer os.RemoveAll(outDir)
	}
	if err != nil {
		return result.RunOutcome{}, fmt.Errorf("runner: report dir: %w", err)
	}
	oc, err := d.sandboxRun(ctx, withReportChannel(s, outDir, token, code))
	if err != nil {
		return result.RunOutcome{}, fmt.Errorf("runner: run: %w", err)
	}
	crash, report, note, err := collectReport(outDir, token, code, oc.ExitCode, d.parse)
	if err != nil {
		return result.RunOutcome{}, fmt.Errorf("runner: %w", err)
	}
	return result.RunOutcome{
		ExitCode:   oc.ExitCode,
		TimedOut:   oc.TimedOut,
		OOMKilled:  oc.OOMKilled,
		Duration:   oc.Duration,
		Crash:      crash,
		Note:       note,
		Report:     tail(report, ReportTailBytes),
		StderrTail: tail(oc.Stderr, StderrTailBytes),
	}, nil
}

// newSecret returns a random log dir name and a random sanitizer exit code.
func newSecret() (string, int, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", 0, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(sanitizerExits))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(b), minSanitizerExit + int(n.Int64()), nil
}

// makeOutDir makes dir (mode 0711: the run user may enter it but not list
// it) and dir/token (mode 2733: anyone may create files, only we may list).
// The setgid bit gives new files our group, so we can read them back even
// though the run user owns them.
func makeOutDir(cacheDir, token string) (string, error) {
	root := filepath.Join(cacheDir, "tmp")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "out-")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return dir, err
	}
	sub := filepath.Join(dir, token)
	if err := os.Mkdir(sub, 0o700); err != nil {
		return dir, err
	}
	return dir, os.Chmod(sub, 0o733|os.ModeSetgid)
}

// withReportChannel returns a copy of s that mounts outDir and points the
// sanitizers at the secret log dir and exit code. Our options come last,
// so they win over the same options in the profile env.
func withReportChannel(s sandbox.Spec, outDir, token string, code int) sandbox.Spec {
	env := make(map[string]string, len(s.Env)+2)
	for k, v := range s.Env {
		env[k] = v
	}
	logDir := RunOutDir + "/" + token
	common := fmt.Sprintf("exitcode=%d:abort_on_error=0", code)
	env["ASAN_OPTIONS"] = joinOpts(env["ASAN_OPTIONS"], "log_path="+logDir+"/asan:"+common)
	env["UBSAN_OPTIONS"] = joinOpts(env["UBSAN_OPTIONS"], "log_path="+logDir+"/ubsan:"+common)
	s.Env = env
	s.Mounts = append(append([]sandbox.Mount(nil), s.Mounts...), sandbox.Mount{Source: outDir, Target: RunOutDir})
	return s
}

func joinOpts(base, extra string) string {
	base = strings.TrimSuffix(base, ":")
	if base == "" {
		return extra
	}
	return base + ":" + extra
}

// collectReport reads the sanitizer log files of one run. A crash counts
// only when a log file holds a sanitizer report and the exit code is the
// secret one. note explains a report that was not counted.
func collectReport(outDir, token string, want, exit int, parse func(string) *result.Crash) (crash *result.Crash, report, note string, err error) {
	sub := filepath.Join(outDir, token)
	entries, err := os.ReadDir(sub) // sorted by name
	if err != nil {
		return nil, "", "", fmt.Errorf("read report dir: %w", err)
	}
	var notes []string
	files := 0
	for _, e := range entries {
		b, why := readReport(filepath.Join(sub, e.Name()))
		if why != "" {
			notes = append(notes, why)
			continue
		}
		files++
		if c := parse(string(b)); c != nil && crash == nil {
			crash, report = c, string(b)
		}
	}
	if crash != nil && exit != want {
		notes = append(notes, fmt.Sprintf("a sanitizer report was written but the exit code was %d, not the sanitizer exit code, so it was not counted", exit))
		crash, report = nil, ""
	}
	if crash == nil && files > 0 && len(notes) == 0 {
		notes = append(notes, "the sanitizer wrote a log file without a crash report")
	}
	return crash, report, strings.Join(notes, "; "), nil
}

// readReport reads one log file. It never follows symlinks and never
// blocks on a FIFO: the run user owns these files and could plant either.
// why is set when the file is skipped.
func readReport(path string) (b []byte, why string) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Sprintf("log file %q could not be opened, skipped", filepath.Base(path))
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Sprintf("log file %q is not a regular file, skipped", filepath.Base(path))
	}
	b, err = io.ReadAll(io.LimitReader(f, MaxReportBytes))
	if err != nil {
		return nil, fmt.Sprintf("log file %q could not be read, skipped", filepath.Base(path))
	}
	return b, ""
}
