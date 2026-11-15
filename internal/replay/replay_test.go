package replay

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// writeFile creates a file in dir with the given name and contents.
func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

// freePort picks an unused TCP port on loopback.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// dialTCP connects to the given address with a short timeout and returns a
// *net.TCPConn so the caller can use CloseWrite for half-close.
func dialTCP(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		t.Fatal("expected *net.TCPConn")
	}
	return tc
}

func TestSingleServer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "greeting.txt", "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Send: "greeting.txt"},
			{Read: "line"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Read the response the server sends.
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := string(buf[:n])
	want := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Send a line back so the server's read step completes.
	if _, err := conn.Write([]byte("done\n")); err != nil {
		t.Fatal(err)
	}

	// Close the write side so the server's post-steps waitForEOF sees EOF
	// and closes the connection promptly.
	if err := conn.CloseWrite(); err != nil {
		t.Fatal(err)
	}

	// The server should close after all steps. Read until EOF.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = conn.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after steps complete, got %v", err)
	}
}

func TestMultipleServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "AAA")
	writeFile(t, dir, "b.txt", "BBB")

	p1 := freePort(t)
	p2 := freePort(t)
	servers := []spec.Server{
		{Port: p1, Steps: []spec.Step{{Send: "a.txt"}}},
		{Port: p2, Steps: []spec.Step{{Send: "b.txt"}}},
	}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Connect to both servers and verify they each send the right file.
	for _, tc := range []struct {
		port int
		want string
	}{
		{p1, "AAA"},
		{p2, "BBB"},
	} {
		conn := dialTCP(t, g.Addr(tc.port))

		// Close write side immediately -- we have nothing to send, and this
		// lets the server's post-steps waitForEOF finish quickly.
		conn.CloseWrite()

		data, err := io.ReadAll(conn)
		conn.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != tc.want {
			t.Errorf("port %d: got %q, want %q", tc.port, data, tc.want)
		}
	}
}

func TestReadLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.txt", "OK")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Read: "line"},
			{Send: "ok.txt"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Send partial data without a newline. The server should still be
	// waiting on the read step.
	conn.Write([]byte("partial"))

	// Give the server a moment to process, then check that nothing was
	// sent back yet (the send step should not have started).
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("server sent data before newline was received")
	}
	if !isTimeout(err) {
		t.Fatalf("expected timeout, got %v", err)
	}

	// Now send the newline. The server should proceed to the send step.
	conn.SetReadDeadline(time.Time{})
	conn.Write([]byte("\n"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "OK" {
		t.Fatalf("got %q, want %q", buf[:n], "OK")
	}
}

func TestReadHeaders(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.txt", "OK")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Read: "headers"},
			{Send: "ok.txt"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Send headers without the final blank line.
	conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n"))

	// The server should still be waiting.
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	if !isTimeout(err) {
		t.Fatalf("expected timeout while headers are incomplete, got %v", err)
	}

	// Complete the headers.
	conn.SetReadDeadline(time.Time{})
	conn.Write([]byte("\r\n"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "OK" {
		t.Fatalf("got %q, want %q", buf[:n], "OK")
	}
}

func TestReadAny(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.txt", "OK")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Read: "any"},
			{Send: "ok.txt"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Send a single byte -- the server should immediately proceed.
	conn.Write([]byte("x"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "OK" {
		t.Fatalf("got %q, want %q", buf[:n], "OK")
	}
}

func TestSleepStep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.txt", "OK")

	port := freePort(t)
	sleepMS := 300
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{SleepMS: sleepMS},
			{Send: "ok.txt"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	start := time.Now()
	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Wait for the data that comes after the sleep.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "OK" {
		t.Fatalf("got %q, want %q", buf[:n], "OK")
	}
	if elapsed < time.Duration(sleepMS)*time.Millisecond {
		t.Fatalf("data arrived too soon: %v (expected >= %dms)", elapsed, sleepMS)
	}
}

func TestCloseStep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "partial.txt", "HELLO")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Send: "partial.txt"},
			{Close: true},
			// After close-write, the server continues but has no more
			// steps. The handler will waitForEOF.
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Read all data -- the server sends "HELLO" then half-closes.
	data, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "HELLO" {
		t.Fatalf("got %q, want %q", data, "HELLO")
	}

	// The client should still be able to send data. The server is
	// waiting for EOF in its post-steps drain.
	_, err = conn.Write([]byte("still open\n"))
	if err != nil {
		t.Fatalf("write after half-close should succeed: %v", err)
	}
}

func TestStopCancelsConnections(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.txt", "OK")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Send: "ok.txt"},
			{SleepMS: spec.MaxSleepMS}, // 10 seconds -- will be interrupted
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}

	conn := dialTCP(t, g.Addr(port))
	defer conn.Close()

	// Read the initial send so we know the connection is up.
	buf := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "OK" {
		t.Fatalf("got %q, want %q", buf[:n], "OK")
	}

	// Stop should return promptly, not wait for the 10s sleep.
	start := time.Now()
	g.Stop()
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("Stop took %v, expected < 3s", elapsed)
	}
}

func TestMultipleConnections(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "resp.txt", "RESPONSE")

	port := freePort(t)
	servers := []spec.Server{{
		Port: port,
		Steps: []spec.Step{
			{Read: "line"},
			{Send: "resp.txt"},
		},
	}}

	g, err := New(servers, dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Stop()

	// Two concurrent clients.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	results := make([]string, 2)

	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", g.Addr(port), 2*time.Second)
			if err != nil {
				errs[i] = err
				return
			}
			conn := c.(*net.TCPConn)
			defer conn.Close()

			// Send the line the server is waiting for.
			if _, err := fmt.Fprintf(conn, "request %d\n", i); err != nil {
				errs[i] = err
				return
			}

			// Close write side so the server's waitForEOF sees EOF.
			conn.CloseWrite()

			// Read everything the server sends.
			data, err := io.ReadAll(conn)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = string(data)
		}()
	}

	wg.Wait()

	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("client %d error: %v", i, errs[i])
		}
		if results[i] != "RESPONSE" {
			t.Errorf("client %d got %q, want %q", i, results[i], "RESPONSE")
		}
	}
}

// isTimeout reports whether the error is a network timeout.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
