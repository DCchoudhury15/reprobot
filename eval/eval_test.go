//go:build eval

// Package eval runs reprobot on real, fixed bugs in real projects.
//
// For every real bug the same report must crash as claimed on the last
// vulnerable commit and must not crash on the fix commit. Reports about
// bugs that do not exist must not crash at all. Two different real bugs
// must never be listed as duplicates of each other.
//
//	go test -tags eval -timeout 60m -v ./eval/
//
// Needs Docker, git and network access to clone the project. Set
// REPROBOT_EVAL_CACHE to keep the git mirror and builds between runs, and
// REPROBOT_EVAL_WRITE=1 to rewrite RESULTS.md in each project dir.
// REPROBOT_EVAL_SITE=1 writes site.json in each project dir and rebuilds
// site/real.js, the recorded runs the project site shows, from all of them.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
)

type caseFile struct {
	Project string      `yaml:"project"`
	Real    []realCase  `yaml:"real"`
	False   []falseCase `yaml:"false"`
}

type realCase struct {
	Dir   string `yaml:"dir"`
	ID    string `yaml:"id"`
	Title string `yaml:"title"` // short name for the site
	Fix   string `yaml:"fix"`   // full id of the fix commit
	Input string `yaml:"input"` // where the proof of concept comes from
}

type falseCase struct {
	Dir     string `yaml:"dir"`
	About   string `yaml:"about"`
	Finding string `yaml:"finding"` // text a claim finding must contain
	Stderr  string `yaml:"stderr"`  // text the program must print on stderr
}

func TestLibxml2(t *testing.T) { runProject(t, "libxml2") }
func TestCurl(t *testing.T)    { runProject(t, "curl") }
func TestJq(t *testing.T)      { runProject(t, "jq") }

func runProject(t *testing.T, name string) {
	dir, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	var cf caseFile
	b, err := os.ReadFile(filepath.Join(dir, "cases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	cache := os.Getenv("REPROBOT_EVAL_CACHE")
	if cache == "" {
		cache = t.TempDir()
	}
	project := filepath.Join(dir, cf.Project)

	var out strings.Builder
	fmt.Fprintf(&out, "# %s results\n\n", name)
	out.WriteString("Written by `go test -tags eval ./eval/`. Times include the build when it was not cached.\n\n")
	out.WriteString("## Real bugs\n\n")
	out.WriteString("| Bug | Vulnerable commit | Verdict | Crash | Signature | Crash line | Time | Fix commit | Verdict | Time |\n")
	out.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")

	type bug struct {
		id    string
		crash *result.Crash
	}
	var bugs []bug
	site := siteProject{Project: name, Recorded: time.Now().UTC().Format("2006-01-02")}
	for _, c := range cf.Real {
		t.Run(c.Dir, func(t *testing.T) {
			repro := filepath.Join(dir, c.Dir, "repro.yaml")
			vuln := check(t, project, repro, cache)
			if vuln.State != result.ReproducedAsClaimed {
				t.Errorf("vulnerable commit: state = %s (%s), want %s; findings %q",
					vuln.State, vuln.Reason, result.ReproducedAsClaimed, vuln.ClaimFindings)
			}
			fixed := check(t, project, atCommit(t, filepath.Join(dir, c.Dir), vuln.Inputs.Commit, c.Fix), cache)
			if fixed.State != result.NotReproduced {
				t.Errorf("fix commit: state = %s (%s), want %s", fixed.State, fixed.Reason, result.NotReproduced)
			}
			if fixed.Inputs.Commit != c.Fix {
				t.Errorf("fix run used commit %s, want %s", fixed.Inputs.Commit, c.Fix)
			}
			crash, sig, line := "", "", ""
			if vuln.Crash != nil {
				crash = vuln.Crash.Kind + " in " + topFrame(vuln.Crash)
				sig = "`" + vuln.Crash.Signature + "`"
				if _, text, ok := strings.Cut(vuln.Crash.SiteText, "|"); ok {
					line = "`" + strings.ReplaceAll(text, "|", `\|`) + "`"
				}
				bugs = append(bugs, bug{c.ID, vuln.Crash})
			}
			fmt.Fprintf(&out, "| %s | `%s` | %s | %s | %s | %s | %s | `%s` | %s | %s |\n",
				c.ID, short(vuln.Inputs.Commit), vuln.State, crash, sig, line, took(vuln),
				short(c.Fix), fixed.State, took(fixed))
			r := siteCase(t, name, filepath.Join(dir, c.Dir), vuln)
			r.ID, r.Title, r.Input = c.ID, c.Title, c.Input
			r.Vuln, r.Fix = siteRun(vuln), siteRun(fixed)
			site.Real = append(site.Real, r)
		})
	}
	// The same rule the store uses: same signature, and the same site or
	// no site to tell them apart.
	for i, a := range bugs {
		for _, b := range bugs[i+1:] {
			if a.crash.Signature == b.crash.Signature &&
				(a.crash.Site == "" || b.crash.Site == "" || a.crash.Site == b.crash.Site) {
				t.Errorf("%s and %s are different bugs but would be listed as duplicates (signature %s, sites %q and %q)",
					a.id, b.id, a.crash.Signature, a.crash.SiteText, b.crash.SiteText)
			}
		}
	}

	out.WriteString("\n## Reports about bugs that do not exist\n\n")
	out.WriteString("| Report | Verdict | Claim findings | Time |\n|---|---|---|---|\n")
	for _, c := range cf.False {
		t.Run(c.Dir, func(t *testing.T) {
			res := check(t, project, filepath.Join(dir, c.Dir, "repro.yaml"), cache)
			if res.State != result.NotReproduced {
				t.Errorf("state = %s (%s), want %s", res.State, res.Reason, result.NotReproduced)
			}
			if c.Finding != "" && !strings.Contains(strings.Join(res.ClaimFindings, "\n"), c.Finding) {
				t.Errorf("claim findings %q do not contain %q", res.ClaimFindings, c.Finding)
			}
			if c.Stderr != "" {
				for i, r := range res.Runs {
					if !strings.Contains(r.StderrTail, c.Stderr) {
						t.Errorf("run %d: stderr does not contain %q: %q", i, c.Stderr, r.StderrTail)
					}
				}
			}
			fmt.Fprintf(&out, "| %s | %s | %s | %s |\n",
				c.About, res.State, strings.Join(res.ClaimFindings, "; "), took(res))
			f := siteCase(t, name, filepath.Join(dir, c.Dir), res)
			run := siteRun(res)
			site.Fakes = append(site.Fakes, siteFake{
				Project: name, Dir: c.Dir, About: c.About, Commit: run.Commit, Claim: f.Claim,
				State: run.State, Reason: run.Reason, RunsMs: run.RunsMs,
				Findings: append([]string{}, res.ClaimFindings...), StderrHasReport: stderrHasReport(res),
			})
		})
	}

	t.Log("\n" + out.String())
	if os.Getenv("REPROBOT_EVAL_WRITE") == "1" && !t.Failed() {
		if err := os.WriteFile(filepath.Join(dir, "RESULTS.md"), []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("REPROBOT_EVAL_SITE") == "1" && !t.Failed() {
		if err := writeSite(dir, site); err != nil {
			t.Fatal(err)
		}
	}
}

func check(t *testing.T, project, repro, cache string) *result.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := runner.Run(ctx, runner.Options{ProjectPath: project, ReproPath: repro, CacheDir: cache})
	if err != nil {
		t.Fatalf("Run %s: %v", repro, err)
	}
	t.Logf("%s at %s: %s (%s)", filepath.Base(filepath.Dir(repro)), short(res.Inputs.Commit), res.State, res.Reason)
	if res.State == result.BuildFailed {
		t.Logf("build log:\n%s", res.BuildLogTail)
	}
	return res
}

// atCommit copies the case dir to a temp dir and swaps the commit in its
// repro.yaml. Nothing else in the report changes. It returns the new
// repro.yaml path.
func atCommit(t *testing.T, caseDir, from, to string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(caseDir)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dst, "repro.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "commit: " + from + "\n"
	if strings.Count(string(b), old) != 1 {
		t.Fatalf("%s must have exactly one line %q", path, strings.TrimSpace(old))
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, "commit: "+to+"\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func topFrame(c *result.Crash) string {
	for _, f := range c.Frames {
		if f.File != "" {
			return f.Function
		}
	}
	return "?"
}

func took(r *result.Result) string {
	d := r.FinishedAt.Sub(r.StartedAt).Round(time.Second)
	if r.BuildCacheHit {
		return d.String() + " (cached build)"
	}
	return d.String()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
