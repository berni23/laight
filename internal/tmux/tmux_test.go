package tmux

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	if os.Getuid() == 0 {
		t.Skip("root connects to any socket")
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
	// A socket nobody may connect to: "Permission denied" on Linux and macOS alike. (A plain
	// file reads as a stale socket on Linux, "no server running".)
	l, err := net.Listen("unix", filepath.Join(sockets, "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(filepath.Join(sockets, "file"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := (Server{Socket: "file"}).Alive("s", "w"); err == nil || errors.Is(err, ErrGone) {
		t.Errorf("a socket tmux can't open: %v", err)
	}
}

// A server laight starts never holds a secret, and Stale names what a window would inherit from
// the server that the env it's given doesn't set: never tmux's own names or read-only ones (#93).
func TestServerEnv(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	s := Server{Socket: fmt.Sprintf("laight-test-env-%d", time.Now().UnixNano())}
	t.Cleanup(func() { s.KillServer() })
	if got := s.Stale(nil); got != nil {
		t.Errorf("no server: Stale = %q", got)
	}
	t.Setenv("LAIGHT_SECRET_LAIGHT_TEST", "s3cret")
	t.Setenv("LAIGHT_TEST_KEPT", "x")
	t.Setenv("LAIGHT_TEST_STALE", "y")
	if err := s.NewSession("e", Window{Name: "w", Cmd: "sleep 600"}); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.run("show-environment", "-g"); strings.Contains(out, "LAIGHT_SECRET_") || !strings.Contains(out, "LAIGHT_TEST_STALE=y") {
		t.Errorf("server env:\n%s", out)
	}
	stale := s.Stale(Environ([]string{"LAIGHT_TEST_KEPT=z", "PATH=/bin"}))
	if !slices.Contains(stale, "LAIGHT_TEST_STALE") || slices.Contains(stale, "LAIGHT_TEST_KEPT") || slices.Contains(stale, "PATH") || slices.Contains(stale, "TERM") {
		t.Errorf("Stale = %q", stale)
	}
	file := filepath.Join(t.TempDir(), "env")
	if err := WriteEnv(file, map[string]string{"A": "1"}, []string{"LAIGHT_TEST_STALE"}); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", EnvCmd(file, `echo "$A-${LAIGHT_TEST_STALE-unset}"`)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "1-unset" {
		t.Errorf("env file: %v %s", err, out)
	}
}
