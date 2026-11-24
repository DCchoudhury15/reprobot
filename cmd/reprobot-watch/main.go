// Command reprobot-watch runs a proof of concept and checks, from outside
// the process, that a sanitizer crash really came from the sanitizer.
//
// Usage: reprobot-watch -code N -out DIR -- CMD [ARG...]
//
// It must be PID 1 of the run container. It writes its verdict to
// DIR/watch.json once every other process is gone, then exits with the
// program's status. See package watch.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/DCchoudhury15/reprobot/internal/watch"
)

func main() {
	code := flag.Int("code", 0, "the sanitizer exit code")
	out := flag.String("out", "", "the secret log dir to write the verdict into")
	flag.Parse()
	if *out == "" || *code <= 0 || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: reprobot-watch -code N -out DIR -- CMD [ARG...]")
		os.Exit(125)
	}

	var v watch.Verdict
	if os.Getpid() != 1 {
		// The program could kill a watcher that is not PID 1.
		v.Error = "reprobot-watch must run as PID 1"
	} else {
		// No other process of the same user may read or change our memory.
		if err := prctl(syscall.PR_SET_DUMPABLE, 0); err != nil {
			v.Error = "prctl: " + err.Error()
		}
		// The kernel drops signals sent to PID 1 from inside its pid
		// namespace when PID 1 has no handler. Go installs handlers that
		// exit on SIGTERM and others, so take them all. The child gets
		// default handlers back on exec.
		sigs := make(chan os.Signal, 64)
		signal.Notify(sigs)
		go func() {
			for range sigs {
			}
		}()
	}
	if v.Error == "" {
		v = watch.Trace(flag.Args(), *code)
	}
	if err := write(*out, v); err != nil {
		fmt.Fprintf(os.Stderr, "reprobot-watch: %v\n", err)
		os.Exit(125)
	}
	if v.Error != "" {
		fmt.Fprintf(os.Stderr, "reprobot-watch: %s\n", v.Error)
		os.Exit(125)
	}
	os.Exit(v.Status)
}

// write stores the verdict. The program knew the dir and may have planted
// a file or a symlink with this name, so remove it first; nothing else is
// running by now.
func write(dir string, v watch.Verdict) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, watch.FileName)
	if err := os.RemoveAll(p); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func prctl(option, arg int) error {
	_, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, uintptr(option), uintptr(arg), 0)
	if e != 0 {
		return e
	}
	return nil
}
