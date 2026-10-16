package tmux

import (
	"fmt"
	"os/exec"
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
