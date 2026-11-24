//go:build linux && amd64

package watch

import (
	"strings"
	"testing"
)

// Outside a container Trace is not PID 1, so it only kills the program.

func TestTraceExitFromLibc(t *testing.T) {
	v := Trace([]string{"sh", "-c", "exit 77"}, 77)
	if v.Error != "" {
		t.Skipf("ptrace not allowed here: %s", v.Error)
	}
	if v.Status != 77 || v.SanitizerExit || v.Pid <= 0 {
		t.Errorf("verdict = %+v", v)
	}
	if !strings.Contains(v.Why, "not from the sanitizer runtime") && !strings.Contains(v.Why, "libc") {
		t.Errorf("why = %q", v.Why)
	}
}

func TestTraceOtherExitCode(t *testing.T) {
	v := Trace([]string{"sh", "-c", "exit 3"}, 77)
	if v.Error != "" {
		t.Skipf("ptrace not allowed here: %s", v.Error)
	}
	if v.Status != 3 || v.SanitizerExit || !strings.Contains(v.Why, "with 3, not the sanitizer exit code") {
		t.Errorf("verdict = %+v", v)
	}
}

// Signals the program sends to itself reach it, and its death by a signal
// is reported like a shell does.
func TestTraceSignals(t *testing.T) {
	v := Trace([]string{"sh", "-c", "trap 'exit 5' USR1; kill -USR1 $$; sleep 5"}, 77)
	if v.Error != "" {
		t.Skipf("ptrace not allowed here: %s", v.Error)
	}
	if v.Status != 5 {
		t.Errorf("trapped signal: verdict = %+v", v)
	}
	v = Trace([]string{"sh", "-c", "kill -TERM $$"}, 77)
	if v.Status != 128+15 || v.SanitizerExit {
		t.Errorf("killed by SIGTERM: verdict = %+v", v)
	}
}

// Child processes and threads must not confuse the watcher.
func TestTraceChildren(t *testing.T) {
	v := Trace([]string{"sh", "-c", "sh -c 'exit 77' & wait; (exit 9); exit 4"}, 77)
	if v.Error != "" {
		t.Skipf("ptrace not allowed here: %s", v.Error)
	}
	if v.Status != 4 || v.SanitizerExit {
		t.Errorf("verdict = %+v", v)
	}
}

func TestTraceMissingProgram(t *testing.T) {
	v := Trace([]string{"/no/such/program"}, 77)
	if v.Status != 127 || v.SanitizerExit || v.Error != "" {
		t.Errorf("verdict = %+v", v)
	}
}
