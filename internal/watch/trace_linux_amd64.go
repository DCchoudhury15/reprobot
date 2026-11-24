//go:build linux && amd64

package watch

import (
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// PTRACE_O_EXITKILL is missing from package syscall.
const ptraceOExitKill = 0x100000

// Trace runs argv as a ptrace child, waits until it and every other process
// in the pid namespace are gone, and says how the program ended. code is
// the sanitizer exit code. Trace is meant to run as PID 1: once the program
// has exited, it kills every process left.
func Trace(argv []string, code int) Verdict {
	// Every ptrace call must come from the thread that started the child.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if len(argv) == 0 {
		return Verdict{Error: "no command"}
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		bin = argv[0]
	}
	pid, err := syscall.ForkExec(bin, argv, &syscall.ProcAttr{
		Env:   os.Environ(),
		Files: []uintptr{0, 1, 2},
		Sys:   &syscall.SysProcAttr{Ptrace: true},
	})
	if err != nil {
		// Like a shell: 127 when the program is missing, 126 otherwise.
		fmt.Fprintf(os.Stderr, "reprobot-watch: %s: %v\n", argv[0], err)
		st := 126
		if errors.Is(err, syscall.ENOENT) {
			st = 127
		}
		return Verdict{Status: st, Why: "the program did not start"}
	}

	t := tracer{main: pid, code: code, traced: map[int]bool{pid: true}, pending: map[int]bool{}}
	t.v.Pid = pid
	t.v.Why = "the program did not end with exit_group"

	// The child stops with SIGTRAP after exec.
	var ws syscall.WaitStatus
	if _, err := wait4(pid, &ws, syscall.WALL); err != nil || !ws.Stopped() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.v.Error = fmt.Sprintf("ptrace: the program did not stop after exec (%v, status %#x)", err, uint32(ws))
		t.reapAll()
		return t.v
	}
	opts := syscall.PTRACE_O_TRACEEXIT | syscall.PTRACE_O_TRACECLONE | syscall.PTRACE_O_TRACEEXEC | ptraceOExitKill
	if err := syscall.PtraceSetOptions(pid, opts); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.v.Error = "ptrace: set options: " + err.Error()
		t.reapAll()
		return t.v
	}
	_ = syscall.PtraceCont(pid, 0)
	t.loop()
	return t.v
}

type tracer struct {
	main    int
	code    int
	traced  map[int]bool // threads of the program we trace
	pending map[int]bool // new threads whose first SIGSTOP is still due
	done    bool         // the program has exited
	v       Verdict
}

// loop waits for every child until none is left.
func (t *tracer) loop() {
	for {
		var ws syscall.WaitStatus
		pid, err := wait4(-1, &ws, syscall.WALL)
		if err == syscall.ECHILD {
			return
		}
		if err != nil {
			t.v.Error = "wait: " + err.Error()
			t.killAll()
			t.reapAll()
			return
		}
		switch {
		case ws.Exited() || ws.Signaled():
			delete(t.traced, pid)
			delete(t.pending, pid)
			if pid == t.main {
				t.v.Status = status(ws)
				t.done = true
			}
		case ws.Stopped():
			t.stop(pid, ws)
		}
		if t.done {
			// The container ends with the program, as it did before
			// the watcher. Nothing else may run while we write.
			t.killAll()
		}
	}
}

// stop handles one ptrace stop and resumes the thread.
func (t *tracer) stop(pid int, ws syscall.WaitStatus) {
	sig := ws.StopSignal()
	if sig == syscall.SIGTRAP {
		switch ws.TrapCause() {
		case syscall.PTRACE_EVENT_EXIT:
			t.exit(pid)
			_ = syscall.PtraceCont(pid, 0)
			return
		case syscall.PTRACE_EVENT_CLONE:
			if msg, err := syscall.PtraceGetEventMsg(pid); err == nil {
				if tid := int(msg); !t.traced[tid] {
					t.traced[tid] = true
					t.pending[tid] = true
				}
			}
			_ = syscall.PtraceCont(pid, 0)
			return
		case syscall.PTRACE_EVENT_EXEC:
			_ = syscall.PtraceCont(pid, 0)
			return
		}
	}
	// A new thread starts with SIGSTOP. Its stop can come before or after
	// the clone event of its parent.
	if !t.traced[pid] {
		t.traced[pid] = true
		if sig == syscall.SIGSTOP {
			_ = syscall.PtraceCont(pid, 0)
			return
		}
	}
	if t.pending[pid] && sig == syscall.SIGSTOP {
		delete(t.pending, pid)
		_ = syscall.PtraceCont(pid, 0)
		return
	}
	switch sig {
	case syscall.SIGSTOP, syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU:
		if groupStop(pid) {
			_ = syscall.PtraceCont(pid, 0)
			return
		}
	}
	_ = syscall.PtraceCont(pid, int(sig))
}

// exit looks at a thread that is about to exit. The only exit that counts
// is exit_group, from a thread of the program, with the sanitizer exit
// code, called from sanitizer runtime code.
func (t *tracer) exit(pid int) {
	if t.v.SanitizerExit {
		return
	}
	var regs syscall.PtraceRegs
	if err := syscall.PtraceGetRegs(pid, &regs); err != nil {
		return
	}
	if regs.Orig_rax != syscall.SYS_EXIT_GROUP || tgid(pid) != t.main {
		return
	}
	if int(regs.Rdi&0xff) != t.code&0xff {
		t.v.Why = fmt.Sprintf("the program called exit_group with %d, not the sanitizer exit code", int32(regs.Rdi))
		return
	}
	maps, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/maps")
	if err != nil {
		t.v.Why = "could not read the memory map at exit: " + err.Error()
		return
	}
	// rip is the instruction after the 2 byte syscall instruction.
	t.v.SanitizerExit, t.v.Why = FromSanitizer(regs.Rip-2, ParseMaps(maps), elf.Open)
}

// killAll kills every process in the pid namespace but us. Outside PID 1
// (in tests) kill(-1) would reach every process of the user, so it only
// kills the program.
func (t *tracer) killAll() {
	if os.Getpid() == 1 {
		_ = syscall.Kill(-1, syscall.SIGKILL)
		return
	}
	_ = syscall.Kill(t.main, syscall.SIGKILL)
}

// reapAll waits until no child is left, resuming any that stop.
func (t *tracer) reapAll() {
	for {
		var ws syscall.WaitStatus
		pid, err := wait4(-1, &ws, syscall.WALL)
		if err != nil {
			return
		}
		if ws.Stopped() {
			_ = syscall.PtraceCont(pid, 0)
		}
	}
}

func wait4(pid int, ws *syscall.WaitStatus, opts int) (int, error) {
	for {
		p, err := syscall.Wait4(pid, ws, opts, nil)
		if err != syscall.EINTR {
			return p, err
		}
	}
}

// groupStop says whether a stop is a group-stop rather than a signal about
// to be delivered: PTRACE_GETSIGINFO fails with EINVAL only in group-stop.
func groupStop(pid int) bool {
	var info [128]byte
	_, _, e := syscall.Syscall6(syscall.SYS_PTRACE, syscall.PTRACE_GETSIGINFO, uintptr(pid), 0, uintptr(unsafe.Pointer(&info[0])), 0, 0)
	return e == syscall.EINVAL
}

// tgid is the process id of a thread, or 0 when it cannot be read.
func tgid(tid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(tid) + "/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Tgid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// status is an exit code, or 128 + signal, like a shell reports it.
func status(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}
