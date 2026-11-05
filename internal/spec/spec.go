// Package spec holds the input formats: the reporter's repro.yaml and the
// maintainer's project config. See docs/DESIGN.md.
package spec

// Reproducer is the reporter file (repro.yaml).
type Reproducer struct {
	Version int      `yaml:"version" json:"version"`
	Project string   `yaml:"project" json:"project"`
	Commit  string   `yaml:"commit" json:"commit"`
	Profile string   `yaml:"profile" json:"profile"`
	Run     Run      `yaml:"run" json:"run"`
	Files   []string `yaml:"files" json:"files"`
	Claim   Claim    `yaml:"claim" json:"claim"`
}

// Run says how to trigger the bug inside the sandbox.
type Run struct {
	Cmd            []string `yaml:"cmd" json:"cmd"`
	TimeoutSeconds int      `yaml:"timeout_seconds" json:"timeout_seconds"`
	Repeat         int      `yaml:"repeat" json:"repeat"`
}

// Claim is what the reporter says will happen.
type Claim struct {
	Sanitizer string `yaml:"sanitizer" json:"sanitizer"` // address | undefined | none
	Kind      string `yaml:"kind" json:"kind"`
	Frame     string `yaml:"frame" json:"frame"`
	Text      string `yaml:"text" json:"text"`
}

// Project is the maintainer-owned config. Reporters can only pick a profile
// by name.
type Project struct {
	Version  int                `yaml:"version" json:"version"`
	ID       string             `yaml:"id" json:"id"`
	Repo     string             `yaml:"repo" json:"repo"`
	Image    string             `yaml:"image" json:"image"`
	Profiles map[string]Profile `yaml:"profiles" json:"profiles"`
	Limits   Limits             `yaml:"limits" json:"limits"`
}

// Profile is one way to build the project, for example with ASan.
type Profile struct {
	Build   string            `yaml:"build" json:"build"`
	Outputs []string          `yaml:"outputs" json:"outputs"`
	Env     map[string]string `yaml:"env" json:"env"`
}

// Limits apply to both build and run sandboxes.
type Limits struct {
	CPUs                float64 `yaml:"cpus" json:"cpus"`
	MemoryMB            int     `yaml:"memory_mb" json:"memory_mb"`
	BuildTimeoutSeconds int     `yaml:"build_timeout_seconds" json:"build_timeout_seconds"`
}

// Defaults and hard maximums for reporter-controlled values.
const (
	DefaultTimeoutSeconds = 30
	MaxTimeoutSeconds     = 300
	DefaultRepeat         = 5
	MaxRepeat             = 20
)
