package spec

import "errors"

// ErrNotImplemented marks a stub that another part of the work will fill in.
var ErrNotImplemented = errors.New("not implemented")

// LoadReproducer reads a repro.yaml file, rejects unknown fields, validates
// it and fills in defaults (timeout, repeat).
func LoadReproducer(path string) (*Reproducer, error) { return nil, ErrNotImplemented }

// LoadProject reads a project config file, rejects unknown fields and
// validates it.
func LoadProject(path string) (*Project, error) { return nil, ErrNotImplemented }

// CheckAgainst checks that the reproducer fits the project: same project id
// and a profile name the project defines.
func (r *Reproducer) CheckAgainst(p *Project) error { return ErrNotImplemented }

// FilePaths returns absolute paths of the reporter files. Every file must
// stay inside baseDir: no absolute paths, no "..", no symlink that leaves
// baseDir.
func (r *Reproducer) FilePaths(baseDir string) ([]string, error) { return nil, ErrNotImplemented }

// ReproducerHash returns a sha256 hex over the repro.yaml bytes and the
// reporter files, so the same input always gives the same hash.
func ReproducerHash(reproPath string, r *Reproducer) (string, error) {
	return "", ErrNotImplemented
}
