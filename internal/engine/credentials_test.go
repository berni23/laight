package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
// in tick, outside the run.json lock.
func TestFailureRecheckCredential(t *testing.T) {
	for _, c := range []struct {
		name  string
		valid bool
		kind  string
	}{{"expired", false, state.KindCredential}, {"valid", true, state.KindExit}} {
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
			if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: spec.ExitFailed, Out: map[string]any{"reason": "AccessDenied"}}); err != nil {
				t.Fatal(err)
			}
			status, reason, err := e.tick(ctx, id)
			if err != nil || status != state.Failed {
				t.Fatalf("tick: %q %v", status, err)
			}
			r := load(t, e, id)
			if r.Failure == nil || r.Failure.Kind != c.kind || r.Failure.Step != "one" {
				t.Fatalf("failure %+v", r.Failure)
			}
			if c.valid {
				return
			}
			want := "credential aws fails its check (exit 1: session expired); step one failed: AccessDenied"
			if r.Failure.Reason != want || reason != want || r.Error != want {
				t.Errorf("reason %q, returned %q, run.json %q", r.Failure.Reason, reason, r.Error)
			}
		})
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
