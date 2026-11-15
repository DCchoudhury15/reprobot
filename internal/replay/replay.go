// Package replay implements a scripted TCP replay server for testing network
// clients. Each Server listens on a loopback port and runs a fixed sequence
// of steps (read, send, sleep, close) on every accepted connection.
package replay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// Timeouts and limits.
const (
	readTimeout     = 30 * time.Second
	eofTimeout      = 5 * time.Second
	stopGrace       = 5 * time.Second
	readBufSize     = 4096
	headerDelimiter = "\r\n\r\n"
)

// entry is one configured server together with its resolved send-file data.
type entry struct {
	spec  spec.Server
	files map[string][]byte // step filename -> contents
}

// Group manages a set of TCP replay servers.
type Group struct {
	entries []entry
	mu      sync.Mutex
	lns     []*net.TCPListener
	addrs   map[int]string // port -> listener address
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	err     error
	done    chan struct{}
}

// New validates the server specs and pre-reads all send-step files from
// filesDir. It does not start any listeners.
func New(servers []spec.Server, filesDir string) (*Group, error) {
	if len(servers) == 0 {
		return nil, errors.New("replay: no servers specified")
	}
	if len(servers) > spec.MaxServers {
		return nil, fmt.Errorf("replay: too many servers (%d > %d)", len(servers), spec.MaxServers)
	}

	entries := make([]entry, len(servers))
	seen := make(map[int]bool)

	for i, srv := range servers {
		if srv.Port <= 0 || srv.Port > 65535 {
			return nil, fmt.Errorf("replay: server %d: invalid port %d", i, srv.Port)
		}
		if seen[srv.Port] {
			return nil, fmt.Errorf("replay: duplicate port %d", srv.Port)
		}
		seen[srv.Port] = true

		if len(srv.Steps) == 0 {
			return nil, fmt.Errorf("replay: server %d (port %d): no steps", i, srv.Port)
		}
		if len(srv.Steps) > spec.MaxSteps {
			return nil, fmt.Errorf("replay: server %d (port %d): too many steps (%d > %d)",
				i, srv.Port, len(srv.Steps), spec.MaxSteps)
		}

		e := entry{spec: srv, files: make(map[string][]byte)}
		for j, step := range srv.Steps {
			if err := validateStep(step); err != nil {
				return nil, fmt.Errorf("replay: server %d step %d: %w", i, j, err)
			}
			if step.Send != "" {
				if _, ok := e.files[step.Send]; !ok {
					p := filepath.Join(filesDir, step.Send)
					data, err := os.ReadFile(p)
					if err != nil {
						return nil, fmt.Errorf("replay: server %d step %d: reading %q: %w",
							i, j, step.Send, err)
					}
					e.files[step.Send] = data
				}
			}
		}
		entries[i] = e
	}

	return &Group{
		entries: entries,
		addrs:   make(map[int]string),
		done:    make(chan struct{}),
	}, nil
}

// validateStep checks that exactly one action is set and its value is valid.
func validateStep(s spec.Step) error {
	n := 0
	if s.Read != "" {
		n++
	}
	if s.Send != "" {
		n++
	}
	if s.SleepMS > 0 {
		n++
	}
	if s.Close {
		n++
	}
	if n != 1 {
		return fmt.Errorf("exactly one action must be set (got %d)", n)
	}
	if s.Read != "" {
		switch s.Read {
		case "line", "headers", "any":
		default:
			return fmt.Errorf("unknown read mode %q", s.Read)
		}
	}
	if s.SleepMS > spec.MaxSleepMS {
		return fmt.Errorf("sleep_ms %d exceeds max %d", s.SleepMS, spec.MaxSleepMS)
	}
	return nil
}

// Start binds all listeners on 127.0.0.1 and begins accepting connections.
// The provided context controls the lifetime of the group: when it is
// cancelled, all listeners close and in-flight connections are torn down.
func (g *Group) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.cancel != nil {
		return errors.New("replay: already started")
	}

	gctx, cancel := context.WithCancel(ctx)
	g.cancel = cancel

	// Bind all listeners before accepting anything, so a port conflict is
	// reported before any goroutines are spawned.
	lns := make([]*net.TCPListener, len(g.entries))
	for i, e := range g.entries {
		addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: e.spec.Port}
		ln, err := net.ListenTCP("tcp4", addr)
		if err != nil {
			// Close the ones we already opened.
			for j := 0; j < i; j++ {
				lns[j].Close()
			}
			cancel()
			return fmt.Errorf("replay: port %d: %w", e.spec.Port, err)
		}
		lns[i] = ln
		g.addrs[e.spec.Port] = ln.Addr().String()
	}
	g.lns = lns

	// Launch an accept loop for each listener.
	for i := range g.entries {
		i := i
		g.wg.Add(1)
		go g.acceptLoop(gctx, i)
	}

	// Close the done channel once all accept loops finish.
	go func() {
		g.wg.Wait()
		close(g.done)
	}()

	return nil
}

// Addr returns the listener address string for the given port. It is safe to
// call after Start returns.
func (g *Group) Addr(port int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addrs[port]
}

// Stop closes all listeners, cancels in-flight connections, and waits up to
// 5 seconds for everything to drain. It is idempotent.
func (g *Group) Stop() {
	g.mu.Lock()
	cancel := g.cancel
	lns := g.lns
	g.lns = nil
	g.mu.Unlock()

	if cancel == nil {
		return
	}

	// Close listeners so accept loops exit.
	for _, ln := range lns {
		if ln != nil {
			ln.Close()
		}
	}
	// Cancel the context so sleeps and reads unblock.
	cancel()

	// Wait for all goroutines, but not forever.
	select {
	case <-g.done:
	case <-time.After(stopGrace):
		log.Printf("replay: stop timed out after %v", stopGrace)
	}
}

// Wait blocks until all accept loops and connection handlers have finished.
// It returns the first error encountered by any listener, or nil.
func (g *Group) Wait() error {
	<-g.done
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}

// setErr records the first error.
func (g *Group) setErr(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err == nil {
		g.err = err
	}
}

// acceptLoop accepts connections on lns[i] until the listener is closed or the
// context is cancelled.
func (g *Group) acceptLoop(ctx context.Context, i int) {
	defer g.wg.Done()

	ln := g.lns[i]
	e := g.entries[i]

	// connWg tracks in-flight connection handlers for this listener.
	var connWg sync.WaitGroup
	defer connWg.Wait()

	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			// Check whether we were intentionally closed.
			select {
			case <-ctx.Done():
				return
			default:
			}
			// A genuine error (not just listener closed).
			if !errors.Is(err, net.ErrClosed) {
				g.setErr(fmt.Errorf("replay: port %d accept: %w", e.spec.Port, err))
			}
			return
		}

		connWg.Add(1)
		go func() {
			defer connWg.Done()
			g.handleConn(ctx, conn, e)
		}()
	}
}

// handleConn runs the scripted steps on a single connection.
func (g *Group) handleConn(ctx context.Context, conn *net.TCPConn, e entry) {
	defer conn.Close()

	for _, step := range e.spec.Steps {
		// If the context is done, stop immediately.
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch {
		case step.Read != "":
			if err := doRead(ctx, conn, step.Read); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("replay: read step failed: %v", err)
				}
				return
			}

		case step.Send != "":
			data := e.files[step.Send]
			if err := doSend(ctx, conn, data); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("replay: send step failed: %v", err)
				}
				return
			}

		case step.SleepMS > 0:
			if err := doSleep(ctx, step.SleepMS); err != nil {
				return
			}

		case step.Close:
			// Half-close: shut down the write side, but the connection
			// stays open for reads. A full close happens when we return.
			if err := conn.CloseWrite(); err != nil {
				log.Printf("replay: close-write failed: %v", err)
				return
			}
		}
	}

	// After all steps, wait for the client to close (read until EOF).
	waitForEOF(conn)
}

// doRead reads from the connection according to the mode.
func doRead(ctx context.Context, conn *net.TCPConn, mode string) error {
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck

	buf := make([]byte, readBufSize)
	var accumulated []byte

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := conn.Read(buf)
		if n > 0 {
			switch mode {
			case "any":
				// Got at least 1 byte, done.
				return nil
			case "line":
				accumulated = append(accumulated, buf[:n]...)
				for _, b := range buf[:n] {
					if b == '\n' {
						return nil
					}
				}
			case "headers":
				accumulated = append(accumulated, buf[:n]...)
				// Look for \r\n\r\n in the accumulated data.
				if len(accumulated) >= 4 {
					for j := 0; j <= len(accumulated)-4; j++ {
						if accumulated[j] == '\r' &&
							accumulated[j+1] == '\n' &&
							accumulated[j+2] == '\r' &&
							accumulated[j+3] == '\n' {
							return nil
						}
					}
				}
			}
		}
		if err != nil {
			return fmt.Errorf("read %q: %w", mode, err)
		}
	}
}

// doSend writes data to the connection.
func doSend(ctx context.Context, conn *net.TCPConn, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// Set a generous write deadline.
	if err := conn.SetWriteDeadline(time.Now().Add(readTimeout)); err != nil {
		return err
	}
	defer conn.SetWriteDeadline(time.Time{}) //nolint:errcheck

	_, err := conn.Write(data)
	return err
}

// doSleep pauses for the given number of milliseconds, respecting context
// cancellation.
func doSleep(ctx context.Context, ms int) error {
	t := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForEOF drains the connection until EOF or the timeout expires.
func waitForEOF(conn *net.TCPConn) {
	conn.SetReadDeadline(time.Now().Add(eofTimeout)) //nolint:errcheck
	buf := make([]byte, 512)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			return
		}
	}
}
