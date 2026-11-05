package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
)

// Exit codes of "reprobot run".
const (
	exitAsClaimed = 0 // REPRODUCED_AS_CLAIMED
	exitVerdict   = 1 // any other verdict
	exitUsage     = 2 // bad command line
	exitInfra     = 3 // infrastructure error (Docker, disk, network)
)

const runUsage = `usage: reprobot run --project project.yaml [--cache DIR] [--json] repro.yaml

  --project FILE  maintainer project config (required)
  --cache DIR     git mirrors and build cache
                  (default $XDG_CACHE_HOME/reprobot, else ~/.cache/reprobot)
  --json          print the result as JSON

Flags go before repro.yaml.
Exit codes: 0 reproduced as claimed, 1 any other verdict, 2 usage error,
3 infrastructure error.`

type runArgs struct {
	project string
	cache   string
	json    bool
	repro   string
}

func runCmd(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runMain(ctx, args, os.Stdout, os.Stderr, runner.Run)
}

func runMain(ctx context.Context, args []string, stdout, stderr io.Writer, run func(context.Context, runner.Options) (*result.Result, error)) int {
	a, err := parseRunArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, runUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "reprobot run: %v\n\n%s\n", err, runUsage)
		return exitUsage
	}
	if a.cache == "" {
		if a.cache, err = defaultCacheDir(); err != nil {
			fmt.Fprintf(stderr, "reprobot run: %v\n", err)
			return exitInfra
		}
	}
	res, err := run(ctx, runner.Options{ProjectPath: a.project, ReproPath: a.repro, CacheDir: a.cache})
	if err != nil {
		fmt.Fprintf(stderr, "reprobot run: %v\n", err)
		return exitInfra
	}
	if a.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(stderr, "reprobot run: %v\n", err)
			return exitInfra
		}
	} else {
		printResult(stdout, res)
	}
	return exitCode(res.State)
}

func parseRunArgs(args []string) (runArgs, error) {
	var a runArgs
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.project, "project", "", "")
	fs.StringVar(&a.cache, "cache", "", "")
	fs.BoolVar(&a.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	switch {
	case a.project == "":
		return a, errors.New("--project is required")
	case fs.NArg() == 0:
		return a, errors.New("missing repro.yaml")
	case fs.NArg() > 1:
		return a, fmt.Errorf("unexpected arguments after %s: %s (flags go before repro.yaml)",
			fs.Arg(0), strings.Join(fs.Args()[1:], " "))
	}
	a.repro = fs.Arg(0)
	return a, nil
}

// defaultCacheDir is $XDG_CACHE_HOME/reprobot, else ~/.cache/reprobot.
func defaultCacheDir() (string, error) {
	if d := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "reprobot"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no default cache dir, use --cache: %w", err)
	}
	return filepath.Join(home, ".cache", "reprobot"), nil
}

func exitCode(s result.State) int {
	if s == result.ReproducedAsClaimed {
		return exitAsClaimed
	}
	return exitVerdict
}

func printResult(w io.Writer, r *result.Result) {
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(w, "%-10s %s\n", label+":", value)
		}
	}
	line("state", string(r.State))
	line("reason", r.Reason)

	if c := r.Crash; c != nil {
		fmt.Fprintln(w)
		desc := strings.TrimSpace(c.Sanitizer + " " + c.Kind)
		if c.Access != "" {
			desc += ", " + c.Access
			if c.Size > 0 {
				desc += fmt.Sprintf(" of size %d", c.Size)
			}
		}
		line("crash", desc)
		if frames := topFrames(c.Frames, 3); len(frames) > 0 {
			fmt.Fprintln(w, "frames:")
			for _, f := range frames {
				fmt.Fprintf(w, "  #%d %s\n", f.Index, frameText(f))
			}
		}
		line("signature", c.Signature)
		if c.SignatureText != "" {
			fmt.Fprintf(w, "%-10s %s\n", "", c.SignatureText)
		}
	}

	if len(r.ClaimFindings) > 0 {
		fmt.Fprintln(w, "\nclaim findings:")
		for _, f := range r.ClaimFindings {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}

	if r.BuildLogTail != "" {
		fmt.Fprintln(w, "\nbuild log (end):")
		for _, l := range strings.Split(strings.TrimRight(r.BuildLogTail, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}

	fmt.Fprintln(w)
	commit := r.Inputs.Commit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	line("commit", commit)
	line("image", r.Inputs.ImageDigest)
	line("runtime", r.Inputs.SandboxRuntime)
	if r.Inputs.BuildCacheKey != "" {
		hit := "no"
		if r.BuildCacheHit {
			hit = "yes"
		}
		line("cache hit", hit)
	}
	if !r.StartedAt.IsZero() && !r.FinishedAt.IsZero() {
		line("duration", r.FinishedAt.Sub(r.StartedAt).Round(100*time.Millisecond).String())
	}
}

// runtimeFrame reports whether a frame belongs to the sanitizer runtime or
// libc, not to the project.
func runtimeFrame(f result.Frame) bool {
	for _, p := range []string{"__asan", "__interceptor", "__sanitizer", "__ubsan", "__lsan", "__libc_", "_start"} {
		if strings.HasPrefix(f.Function, p) {
			return true
		}
	}
	for _, s := range []string{"libsanitizer/", "compiler-rt/", "sysdeps/", "csu/"} {
		if strings.Contains(f.File, s) {
			return true
		}
	}
	return f.File == "" || strings.HasPrefix(f.File, "/usr/")
}

// topFrames returns the first n project frames, or the first n frames when
// none look like project code.
func topFrames(frames []result.Frame, n int) []result.Frame {
	var out []result.Frame
	for _, f := range frames {
		if !runtimeFrame(f) && len(out) < n {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		out = frames[:min(n, len(frames))]
	}
	return out
}

func frameText(f result.Frame) string {
	where := f.File
	if where != "" && f.Line > 0 {
		where += fmt.Sprintf(":%d", f.Line)
	}
	if where == "" {
		where = f.Module
	}
	return strings.TrimSpace(f.Function + " " + where)
}
