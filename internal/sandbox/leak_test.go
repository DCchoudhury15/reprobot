package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeDaemon puts a fake docker on PATH that acts like the real daemon in
// one way that matters: once a create request is sent, the container is
// made even when the docker client is killed while it waits.
const fakeDaemon = `#!/bin/sh
state="$FAKE_DOCKER_STATE"
case "$1" in
info) echo '{"runc":{}}' ;;
create)
	# The daemon makes the container after 300ms, whatever the client does.
	(sleep 0.3; touch "$state/created") </dev/null >/dev/null 2>&1 &
	exec sleep 0.6 ;;
rm)
	if [ -e "$state/created" ]; then rm "$state/created"; else echo "No such container" >&2; exit 1; fi ;;
*) exit 1 ;;
esac
`

// When ctx ends while docker create is still running, the container must
// still be removed. Before the fix the client was killed, rm ran before the
// daemon had made the container, and the container was left behind.
func TestRunCancelDuringCreateLeavesNoContainer(t *testing.T) {
	bin := t.TempDir()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDaemon), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_STATE", state)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, Spec{Image: "img", Cmd: []string{"true"}}); err == nil {
		t.Fatal("Run returned no error after cancel")
	}
	time.Sleep(500 * time.Millisecond) // let the fake daemon finish
	if _, err := os.Stat(filepath.Join(state, "created")); err == nil {
		t.Fatal("a container was left behind")
	}
}
