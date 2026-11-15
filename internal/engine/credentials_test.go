package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

func TestCheckCredential(t *testing.T) {
	dir := t.TempDir()
	if err := CheckCredential(context.Background(), dir, spec.Credential{Check: "test -d ."}); err != nil {
		t.Errorf("passing check: %v", err)
	}
	token := "ghp_" + strings.Repeat("aB3", 12)
	err := CheckCredential(context.Background(), dir, spec.Credential{Check: "echo noise; echo 'Token expired: " + token + "' >&2; exit 255"})
	if err == nil || !strings.HasPrefix(err.Error(), "exit 255: Token expired:") || strings.Contains(err.Error(), token) {
		t.Errorf("failing check: %v", err)
	}
	// No terminal: a check that reads one gets EOF instead of hanging.
	if err := CheckCredential(context.Background(), dir, spec.Credential{Check: "read x"}); err == nil {
		t.Error("a check reading stdin passed")
	}
	// A check that passes but leaves a child holding its output still passes, and the child is
	// stopped with it.
	pidFile := filepath.Join(dir, "child")
	if err := CheckCredential(context.Background(), dir, spec.Credential{Check: "sleep 30 & echo $! > " + pidFile + "; exit 0"}); err != nil {
		t.Errorf("check leaving a child: %v", err)
	}
	if b, err := os.ReadFile(pidFile); err != nil {
		t.Error(err)
	} else if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid <= 0 || !gone(pid, 3*time.Second) {
		t.Errorf("child %q still running", b)
	}
	defer func(d time.Duration) { CheckTimeout = d }(CheckTimeout)
	CheckTimeout = 200 * time.Millisecond
	start := time.Now()
	err = CheckCredential(context.Background(), dir, spec.Credential{Check: "sleep 30 & wait"})
	if err == nil || !strings.Contains(err.Error(), "no answer in") || time.Since(start) > 5*time.Second {
		t.Errorf("hanging check: %v after %s", err, time.Since(start))
	}
}

// A step that fails while a credential's check fails is blamed on the access (kind credential),
// with the check's reason; while every check passes, it keeps its own kind. The re-check runs
// in tick, outside the run.json lock, and the run stays live until the next tick ends it, so an
// abort or a dead engine meanwhile is handled as for any live run.
func TestFailureRecheckCredential(t *testing.T) {
	for _, c := range []struct {
		name  string
		valid bool
		done  state.Done
		kind  string
		cause string
	}{
		{"expired", false, state.Done{Exit: spec.ExitFailed, Out: map[string]any{"reason": "AccessDenied"}}, state.KindCredential, "AccessDenied"},
		{"expired, timeout", false, state.Done{Exit: spec.ExitFailed, Engine: true, Kind: state.KindTimeout, Out: map[string]any{"error": "quiet for 90m"}}, state.KindCredential, "quiet for 90m"},
		{"valid", true, state.Done{Exit: spec.ExitFailed, Out: map[string]any{"reason": "AccessDenied"}}, state.KindExit, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, id := hooked(t, two(nil), "a")
			mark := filepath.Join(t.TempDir(), "session")
			if c.valid {
				os.WriteFile(mark, nil, 0o600)
			}
			state.Update(e.Home, id, func(r *state.Run) error {
				r.Repo = t.TempDir()
				r.Spec.Credentials = map[string]spec.Credential{
					"aws":    {Check: "test -f " + mark + " || { echo 'session expired' >&2; exit 1; }"},
					"github": {Check: "true"},
				}
				return nil
			})
			ctx := context.Background()
			if _, _, err := e.tick(ctx, id); err != nil {
				t.Fatal(err)
			}
			d := c.done
			d.Step, d.Hop = "one", 1
			if err := state.WriteDone(e.Home, id, d); err != nil {
				t.Fatal(err)
			}
			status, _, err := e.tick(ctx, id)
			if err != nil || status != "" {
				t.Fatalf("re-checking tick: %q %v", status, err)
			}
			if r := load(t, e, id); !r.Live() || r.Failure == nil || r.Failure.Kind != c.kind {
				t.Fatalf("after the re-check: status %s, failure %+v", r.Status, r.Failure)
			}
			status, reason, err := e.tick(ctx, id)
			if err != nil || status != state.Failed {
				t.Fatalf("next tick: %q %v", status, err)
			}
			r := load(t, e, id)
			if r.Failure.Kind != c.kind || r.Failure.Step != "one" {
				t.Fatalf("failure %+v", r.Failure)
			}
			if c.valid {
				return
			}
			want := "credential aws fails its check (exit 1: session expired); step one failed: " + c.cause
			if r.Failure.Reason != want || reason != want || r.Error != want {
				t.Errorf("reason %q, returned %q, run.json %q", r.Failure.Reason, reason, r.Error)
			}
		})
	}
}

// The re-check runs every check at once: the run waits one CheckTimeout at most, not one per
// credential.
func TestRecheckParallel(t *testing.T) {
	defer func(d time.Duration) { CheckTimeout = d }(CheckTimeout)
	CheckTimeout = 500 * time.Millisecond
	r := &state.Run{Repo: t.TempDir(), Spec: &spec.Workflow{Credentials: map[string]spec.Credential{
		"a": {Check: "sleep 5"}, "b": {Check: "sleep 5"}, "c": {Check: "sleep 5"},
	}}}
	start := time.Now()
	name, err := expiredCredential(context.Background(), r)
	if name != "a" || err == nil || time.Since(start) > 1200*time.Millisecond {
		t.Errorf("%s %v after %s", name, err, time.Since(start))
	}
}

// A template that didn't render is the workflow's fault whatever the access: never re-checked.
func TestFailureRecheckSkipsConfig(t *testing.T) {
	e, id := hooked(t, []*spec.Step{{ID: "g", Type: spec.TypeGate, Ask: "Merge {{ .steps.g.out.pr }}?", AllowedExits: []string{"yes"}}})
	state.Update(e.Home, id, func(r *state.Run) error {
		r.Repo = t.TempDir()
		r.Spec.Credentials = map[string]spec.Credential{"aws": {Check: "false"}}
		return nil
	})
	for range 3 {
		if status, _, err := e.tick(context.Background(), id); err != nil || status != "" {
			break
		}
	}
	if f := load(t, e, id).Failure; f == nil || f.Kind != state.KindConfig {
		t.Fatalf("failure %+v", f)
	}
}

// gone waits up to d for pid to exit and be reaped (an orphan is reaped by init, not us).
func gone(pid int, d time.Duration) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
	}
	return false
}
