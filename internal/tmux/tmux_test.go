package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two pastes into the same window at once (the engine delivering a step while a person types) must
// both land: each needs a buffer of its own.
func TestConcurrentPaste(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	s := Server{Socket: fmt.Sprintf("laight-test-paste-%d", time.Now().UnixNano())}
	t.Cleanup(func() { s.KillServer() })
	if err := s.NewSession("p", Window{Name: "w", Cmd: "cat"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Paste("p", "w", fmt.Sprintf("line %d", i))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("paste: %v", err)
		}
	}
	if out, _ := s.run("list-buffers", "-F", "#{buffer_name}"); strings.Contains(out, "laight-") {
		t.Errorf("buffers left behind: %s", out)
	}
}

// A missing server is gone; a socket tmux can't open is "can't say", never "gone": a caller
// would report a crashed seat or a dead engine.
func TestAliveCantConnect(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "lt") // short: a socket path is capped near 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	sockets := filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.Mkdir(sockets, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Server{Socket: "missing"}).Alive("s", "w"); !errors.Is(err, ErrGone) {
		t.Errorf("no server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sockets, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Server{Socket: "file"}).Alive("s", "w"); err == nil || errors.Is(err, ErrGone) {
		t.Errorf("a socket tmux can't open: %v", err)
	}
}
