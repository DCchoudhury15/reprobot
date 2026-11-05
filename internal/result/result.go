// Package result holds what reprobot found. See docs/DESIGN.md.
package result

import "time"

// State is the final verdict for one reproducer.
type State string

const (
	ReproducedAsClaimed State = "REPRODUCED_AS_CLAIMED"
	ReproducedDifferent State = "REPRODUCED_DIFFERENT"
	NotReproduced       State = "NOT_REPRODUCED"
	Flaky               State = "FLAKY"
	Timeout             State = "TIMEOUT"
	OOM                 State = "OOM"
	BuildFailed         State = "BUILD_FAILED"
	InvalidInput        State = "INVALID_INPUT"
	OutOfScope          State = "OUT_OF_SCOPE"
)

// Frame is one line of a sanitizer stack trace.
type Frame struct {
	Index    int    `json:"index"`
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Module   string `json:"module,omitempty"`
}

// Crash is a parsed sanitizer report.
type Crash struct {
	Sanitizer string  `json:"sanitizer"`        // address | undefined
	Kind      string  `json:"kind"`             // heap-buffer-overflow, signed-integer-overflow, ...
	Access    string  `json:"access,omitempty"` // READ | WRITE when known
	Size      int     `json:"size,omitempty"`   // access size in bytes when known
	Frames    []Frame `json:"frames"`
	// Signature is a stable id for "same bug": kind plus the top 3 frames
	// that belong to the project.
	Signature string `json:"signature"`
	// SignatureText is the human-readable input that was hashed.
	SignatureText string `json:"signature_text"`
}

// RunOutcome is one execution of the proof of concept.
type RunOutcome struct {
	ExitCode   int           `json:"exit_code"`
	TimedOut   bool          `json:"timed_out"`
	OOMKilled  bool          `json:"oom_killed"`
	Duration   time.Duration `json:"duration_ns"`
	Crash      *Crash        `json:"crash,omitempty"`
	StderrTail string        `json:"stderr_tail,omitempty"`
}

// Inputs pins exactly what was tested, so a result can be checked later.
type Inputs struct {
	Project        string `json:"project"`
	Commit         string `json:"commit"` // full sha after resolving
	Profile        string `json:"profile"`
	ImageDigest    string `json:"image_digest"`
	ReproducerHash string `json:"reproducer_hash"`
	BuildCacheKey  string `json:"build_cache_key"`
	SandboxRuntime string `json:"sandbox_runtime"` // runsc | runc
}

// Result is the full verdict.
type Result struct {
	State         State        `json:"state"`
	Reason        string       `json:"reason"`
	Inputs        Inputs       `json:"inputs"`
	Runs          []RunOutcome `json:"runs,omitempty"`
	Crashed       int          `json:"crashed"`
	Crash         *Crash       `json:"crash,omitempty"`
	ClaimFindings []string     `json:"claim_findings,omitempty"`
	BuildCacheHit bool         `json:"build_cache_hit"`
	BuildLogTail  string       `json:"build_log_tail,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
}
