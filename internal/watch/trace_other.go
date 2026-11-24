//go:build !(linux && amd64)

package watch

// Trace needs ptrace register access, which is only written for linux/amd64.
func Trace(argv []string, code int) Verdict {
	return Verdict{Error: "the crash check only runs on linux/amd64"}
}
