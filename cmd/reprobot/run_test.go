package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/result"
	"github.com/DCchoudhury15/reprobot/internal/runner"
)

func TestParseRunArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    runArgs
		wantErr bool
	}{
		{"all flags", []string{"--project", "p.yaml", "--cache", "/c", "--json", "r.yaml"},
			runArgs{project: "p.yaml", cache: "/c", json: true, repro: "r.yaml"}, false},
		{"equals form", []string{"-project=p.yaml", "r.yaml"}, runArgs{project: "p.yaml", repro: "r.yaml"}, false},
		{"missing project", []string{"r.yaml"}, runArgs{}, true},
		{"missing repro", []string{"--project", "p.yaml"}, runArgs{}, true},
		{"two repro files", []string{"--project", "p.yaml", "a.yaml", "b.yaml"}, runArgs{}, true},
		{"flag after repro", []string{"--project", "p.yaml", "r.yaml", "--json"}, runArgs{}, true},
		{"unknown flag", []string{"--project", "p.yaml", "--fast", "r.yaml"}, runArgs{}, true},
		{"cache without value", []string{"--project", "p.yaml", "--cache"}, runArgs{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRunArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	if _, err := parseRunArgs([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: err = %v, want flag.ErrHelp", err)
	}
}

func TestExitCodeForEveryState(t *testing.T) {
	states := map[result.State]int{
		result.ReproducedAsClaimed: 0,
		result.ReproducedDifferent: 1,
		result.NotReproduced:       1,
		result.Flaky:               1,
		result.Timeout:             1,
		result.OOM:                 1,
		result.BuildFailed:         1,
		result.InvalidInput:        1,
		result.OutOfScope:          1,
	}
	for state, want := range states {
		var gotOpts runner.Options
		fake := func(_ context.Context, o runner.Options) (*result.Result, error) {
			gotOpts = o
			return &result.Result{State: state, Reason: "r"}, nil
		}
		var out, errOut bytes.Buffer
		code := runMain(context.Background(), []string{"--project", "p.yaml", "--cache", "/c", "r.yaml"}, &out, &errOut, fake)
		if code != want {
			t.Errorf("%s: exit %d, want %d", state, code, want)
		}
		if gotOpts != (runner.Options{ProjectPath: "p.yaml", ReproPath: "r.yaml", CacheDir: "/c"}) {
			t.Errorf("%s: options %+v", state, gotOpts)
		}
		if !strings.Contains(out.String(), string(state)) {
			t.Errorf("%s: output does not show the state:\n%s", state, out.String())
		}
	}
}

func TestRunMainErrors(t *testing.T) {
	called := false
	fake := func(context.Context, runner.Options) (*result.Result, error) {
		called = true
		return nil, errors.New("docker: cannot connect")
	}
	var out, errOut bytes.Buffer
	if code := runMain(context.Background(), []string{"r.yaml"}, &out, &errOut, fake); code != exitUsage {
		t.Errorf("usage error: exit %d, want %d", code, exitUsage)
	}
	if called {
		t.Error("ran despite a usage error")
	}
	if !strings.Contains(errOut.String(), "--project is required") {
		t.Errorf("stderr = %q", errOut.String())
	}

	errOut.Reset()
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "r"}, &out, &errOut, fake); code != exitInfra {
		t.Errorf("infrastructure error: exit %d, want %d", code, exitInfra)
	}
	if !strings.Contains(errOut.String(), "docker: cannot connect") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestDefaultCacheDir(t *testing.T) {
	var got runner.Options
	fake := func(_ context.Context, o runner.Options) (*result.Result, error) {
		got = o
		return &result.Result{State: result.NotReproduced}, nil
	}
	t.Setenv("HOME", "/home/u")
	for xdg, want := range map[string]string{
		"/x/cache": "/x/cache/reprobot",
		"":         "/home/u/.cache/reprobot",
		"relative": "/home/u/.cache/reprobot",
	} {
		t.Setenv("XDG_CACHE_HOME", xdg)
		var out, errOut bytes.Buffer
		runMain(context.Background(), []string{"--project", "p", "r"}, &out, &errOut, fake)
		if got.CacheDir != want {
			t.Errorf("XDG_CACHE_HOME=%q: cache %q, want %q", xdg, got.CacheDir, want)
		}
	}
}

func sampleResult() *result.Result {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &result.Result{
		State:  result.ReproducedAsClaimed,
		Reason: "crashed in 3 of 3 runs, as claimed",
		Inputs: result.Inputs{
			Project: "tinyparse", Commit: "3f2a9c1aaaabbbbccccddddeeeeffff000011112", Profile: "asan",
			ImageDigest: "sha256:abc", BuildCacheKey: "k", SandboxRuntime: "runc",
		},
		Crashed: 3,
		Crash: &result.Crash{
			Sanitizer: "address", Kind: "heap-buffer-overflow", Access: "WRITE", Size: 64,
			Frames: []result.Frame{
				{Index: 0, Function: "__memmove_avx_unaligned_erms", Module: "/lib/x86_64-linux-gnu/libc.so.6"},
				{Index: 1, Function: "__asan_memcpy", File: "../../../../src/libsanitizer/asan/asan_interceptors_memintrinsics.cpp", Line: 63},
				{Index: 2, Function: "parse_header", File: "/src/tinyparse.c", Line: 21},
				{Index: 3, Function: "parse_file", File: "/src/tinyparse.c", Line: 35},
				{Index: 4, Function: "main", File: "/src/tinyparse.c", Line: 47},
				{Index: 5, Function: "__libc_start_main", Module: "/lib/x86_64-linux-gnu/libc.so.6"},
			},
			Signature:     "f00d",
			SignatureText: "heap-buffer-overflow parse_header parse_file main",
		},
		ClaimFindings: []string{`function "parse_blob" does not appear anywhere in the code at 3f2a9c1`},
		BuildCacheHit: true,
		StartedAt:     start,
		FinishedAt:    start.Add(12300 * time.Millisecond),
	}
}

func TestHumanOutput(t *testing.T) {
	var out bytes.Buffer
	printResult(&out, sampleResult())
	got := out.String()
	for _, want := range []string{
		"state:     REPRODUCED_AS_CLAIMED\n",
		"reason:    crashed in 3 of 3 runs, as claimed\n",
		"crash:     address heap-buffer-overflow, WRITE of size 64\n",
		"frames:\n  #2 parse_header /src/tinyparse.c:21\n  #3 parse_file /src/tinyparse.c:35\n  #4 main /src/tinyparse.c:47\nsignature:",
		"signature: f00d\n",
		"heap-buffer-overflow parse_header parse_file main\n",
		`  - function "parse_blob" does not appear anywhere in the code at 3f2a9c1` + "\n",
		"commit:    3f2a9c1\n",
		"image:     sha256:abc\n",
		"runtime:   runc\n",
		"cache hit: yes\n",
		"duration:  12.3s\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "__memmove") || strings.Contains(got, "__asan_memcpy") || strings.Contains(got, "__libc_start_main") {
		t.Errorf("runtime frames shown:\n%s", got)
	}
}

func TestJSONOutput(t *testing.T) {
	want := sampleResult()
	var out, errOut bytes.Buffer
	fake := func(context.Context, runner.Options) (*result.Result, error) { return want, nil }
	if code := runMain(context.Background(), []string{"--project", "p", "--cache", "/c", "--json", "r"}, &out, &errOut, fake); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var got result.Result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.State != want.State || got.Crash.Signature != "f00d" || got.Inputs != want.Inputs {
		t.Errorf("round trip lost data: %+v", got)
	}
	if !strings.Contains(out.String(), "\n  \"state\"") {
		t.Errorf("JSON is not indented:\n%s", out.String())
	}
}
