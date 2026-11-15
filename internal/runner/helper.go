package runner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DCchoudhury15/reprobot/internal/spec"
)

// helperImage is the minimal image for the replay helper.
const helperImage = "alpine:3.21"

// helperBinName is the name of the static replay binary.
const helperBinName = "reprobot-replay"

// helperReady is printed by the helper when all listeners are up.
const helperReady = "READY"

// helperStartTimeout limits how long we wait for the helper to print READY.
const helperStartTimeout = 30 * time.Second

// helper manages a replay helper container.
type helper struct {
	name string
}

// startHelper builds the replay binary (if needed), writes the server spec,
// and starts a helper container that runs the replay servers. The program
// container should use --network=container:<name> to share its network
// namespace. The helper has no external network (network=none, only loopback).
func startHelper(ctx context.Context, cacheDir string, servers []spec.Server, pocDir string) (*helper, error) {
	bin, err := ensureReplayBin(ctx, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("helper: build replay binary: %w", err)
	}

	// Write the spec JSON for the helper.
	specJSON, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("helper: marshal spec: %w", err)
	}
	specPath := filepath.Join(pocDir, ".reprobot-servers.json")
	if err := os.WriteFile(specPath, specJSON, 0o644); err != nil {
		return nil, fmt.Errorf("helper: write spec: %w", err)
	}

	name, err := helperContainerName()
	if err != nil {
		return nil, err
	}

	// Create and start the helper container.
	args := []string{
		"create",
		"--name=" + name,
		"--label=reprobot=1",
		"--log-driver=none",
		"--network=none",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--read-only",
		fmt.Sprintf("--volume=%s:/replay:ro", bin),
		fmt.Sprintf("--volume=%s:/files:ro", pocDir),
		helperImage,
		"/replay",
		"-spec", "/files/.reprobot-servers.json",
		"-files", "/files",
	}
	if _, err := dockerCmd(ctx, args...); err != nil {
		return nil, fmt.Errorf("helper: create: %w", err)
	}
	h := &helper{name: name}

	// Start the container and wait for READY on stdout.
	cmd := exec.CommandContext(ctx, "docker", "start", "-a", name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.cleanup(ctx)
		return nil, fmt.Errorf("helper: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		h.cleanup(ctx)
		return nil, fmt.Errorf("helper: start: %w", err)
	}

	ready := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == helperReady {
				ready <- nil
				return
			}
		}
		if err := sc.Err(); err != nil {
			ready <- err
		} else {
			ready <- errors.New("helper: closed stdout without printing READY")
		}
	}()

	select {
	case err := <-ready:
		if err != nil {
			h.cleanup(ctx)
			return nil, err
		}
	case <-time.After(helperStartTimeout):
		h.cleanup(ctx)
		return nil, errors.New("helper: timed out waiting for READY")
	case <-ctx.Done():
		h.cleanup(ctx)
		return nil, ctx.Err()
	}

	return h, nil
}

// cleanup kills and removes the helper container.
func (h *helper) cleanup(ctx context.Context) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, _ = dockerCmd(cctx, "kill", h.name)
	_, _ = dockerCmd(cctx, "rm", "-f", h.name)
}

// helperContainerName returns a unique container name for a helper.
func helperContainerName() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "reprobot-" + hex.EncodeToString(b) + "-helper", nil
}

// ensureReplayBin builds the static replay binary if it does not exist.
// The binary is stored in cacheDir/bin/reprobot-replay.
func ensureReplayBin(ctx context.Context, cacheDir string) (string, error) {
	binDir := filepath.Join(cacheDir, "bin")
	bin := filepath.Join(binDir, helperBinName)
	if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() {
		return bin, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	// Build with CGO_ENABLED=0 for a static binary that runs in any container.
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/reprobot-replay/")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	// Build from the module root. Find it by looking for go.mod.
	modRoot, err := findModRoot()
	if err != nil {
		return "", err
	}
	cmd.Dir = modRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build reprobot-replay: %w\n%s", err, out)
	}
	return bin, nil
}

// findModRoot walks up from the current executable or working directory to
// find the directory with go.mod.
func findModRoot() (string, error) {
	// Try the executable's directory first.
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	// Fall back to working directory.
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("cannot find module root (go.mod)")
}

// dockerCmd runs a docker command and returns its stdout.
func dockerCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 512 {
			msg = msg[len(msg)-512:]
		}
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, msg)
	}
	return out, nil
}
