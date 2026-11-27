//go:build eval

package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// siteProjects are the projects the site shows, in order. A project is
// left out until its eval passes and writes site.json.
var siteProjects = []string{"libxml2", "jq", "curl", "pcre2"}

type siteProject struct {
	Project  string     `json:"project"`
	Recorded string     `json:"recorded"`
	Real     []siteReal `json:"real"`
	Fakes    []siteFake `json:"fakes"`
}

type siteReal struct {
	Project string     `json:"project"`
	ID      string     `json:"id"`
	Dir     string     `json:"dir"`
	Title   string     `json:"title"`
	Input   string     `json:"input"`
	Cmd     string     `json:"cmd"`
	Files   []siteFile `json:"files"`
	Claim   siteClaim  `json:"claim"`
	Vuln    siteResult `json:"vuln"`
	Fix     siteResult `json:"fix"`
}

type siteFile struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

type siteClaim struct {
	Kind  string `json:"kind"`
	Frame string `json:"frame"`
}

type siteFrame struct {
	Fn string `json:"fn"`
	At string `json:"at"`
}

type siteResult struct {
	Commit    string      `json:"commit"`
	State     string      `json:"state"`
	Reason    string      `json:"reason"`
	Crashed   int         `json:"crashed"`
	RunsMs    []int64     `json:"runsMs"`
	Exit      []int       `json:"exit,omitempty"`
	Kind      string      `json:"kind,omitempty"`
	Access    string      `json:"access,omitempty"`
	Size      int         `json:"size,omitempty"`
	Frames    []siteFrame `json:"frames,omitempty"`
	Signature string      `json:"signature,omitempty"`
	Site      string      `json:"site,omitempty"`
	Line      string      `json:"line,omitempty"`
}

type siteFake struct {
	Project         string    `json:"project"`
	Dir             string    `json:"dir"`
	About           string    `json:"about"`
	Commit          string    `json:"commit"`
	Claim           siteClaim `json:"claim"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	RunsMs          []int64   `json:"runsMs"`
	Findings        []string  `json:"findings"`
	StderrHasReport bool      `json:"stderrHasReport"`
}

// siteCase fills what comes from the report itself: the command, the files
// and the claim.
func siteCase(t *testing.T, project, caseDir string, res *result.Result) siteReal {
	t.Helper()
	repro, err := spec.LoadReproducer(filepath.Join(caseDir, "repro.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r := siteReal{
		Project: project,
		Dir:     filepath.Base(caseDir),
		Cmd:     strings.Join(repro.Run.Cmd, " "),
		Claim:   siteClaim{Kind: repro.Claim.Kind, Frame: repro.Claim.Frame},
		Files:   []siteFile{},
	}
	for _, name := range repro.Files {
		fi, err := os.Stat(filepath.Join(caseDir, name))
		if err != nil {
			t.Fatal(err)
		}
		r.Files = append(r.Files, siteFile{Name: name, Bytes: fi.Size()})
	}
	return r
}

// siteRun keeps what the site shows of one result: the verdict, each
// run's time and exit code, and for a crash its kind, top project frames,
// signature and crash line.
func siteRun(res *result.Result) siteResult {
	s := siteResult{
		Commit: res.Inputs.Commit, State: string(res.State), Reason: res.Reason,
		Crashed: res.Crashed, RunsMs: []int64{},
	}
	for _, r := range res.Runs {
		s.RunsMs = append(s.RunsMs, r.Duration.Milliseconds())
		s.Exit = append(s.Exit, r.ExitCode)
	}
	if c := res.Crash; c != nil {
		s.Kind, s.Access, s.Size, s.Signature, s.Site = c.Kind, c.Access, c.Size, c.Signature, c.Site
		if _, text, ok := strings.Cut(c.SiteText, "|"); ok {
			s.Line = text
		}
		for _, f := range c.Frames {
			if f.File == "" {
				continue
			}
			s.Frames = append(s.Frames, siteFrame{Fn: f.Function, At: fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)})
			if len(s.Frames) == 3 {
				break
			}
		}
	}
	return s
}

func stderrHasReport(res *result.Result) bool {
	for _, r := range res.Runs {
		if strings.Contains(r.StderrTail, "ERROR: AddressSanitizer") {
			return true
		}
	}
	return false
}

// writeSite writes dir/site.json and rebuilds ../site/real.js from the
// site.json of every project in siteProjects that has one.
func writeSite(dir string, p siteProject) error {
	b, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "site.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	all := struct {
		Recorded string     `json:"recorded"`
		Image    string     `json:"image"`
		Real     []siteReal `json:"real"`
		Fakes    []siteFake `json:"fakes"`
	}{Image: "gcc:15", Real: []siteReal{}, Fakes: []siteFake{}}
	root := filepath.Dir(dir)
	for _, name := range siteProjects {
		b, err := os.ReadFile(filepath.Join(root, name, "site.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var sp siteProject
		if err := json.Unmarshal(b, &sp); err != nil {
			return fmt.Errorf("%s/site.json: %w", name, err)
		}
		all.Real = append(all.Real, sp.Real...)
		all.Fakes = append(all.Fakes, sp.Fakes...)
		if sp.Recorded > all.Recorded {
			all.Recorded = sp.Recorded
		}
	}
	js, err := json.MarshalIndent(all, "", " ")
	if err != nil {
		return err
	}
	out := "/* Generated from real runs of eval/ through reprobot (gcc:15 image,\n" +
		"   Docker) by REPROBOT_EVAL_SITE=1 go test -tags eval ./eval/.\n" +
		"   Do not edit by hand. */\nwindow.RB_REAL = " + string(js) + ";\n"
	return os.WriteFile(filepath.Join(filepath.Dir(root), "site", "real.js"), []byte(out), 0o644)
}
