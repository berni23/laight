package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// CheckTimeout bounds a credential's check: one that hangs (a prompt it can't show) fails.
var CheckTimeout = 30 * time.Second

// CheckCredential runs a credential's check in dir, with this process's environment and no
// terminal, and returns nil when it exits 0. The error says why not, with the last line the
// check printed, redacted.
func CheckCredential(ctx context.Context, dir string, c spec.Credential) error {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Check)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &out, &out
	// Its own session, as a shell step's: no terminal for a prompt to stop on, and a process
	// group that a timeout stops whole.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil {
		return nil
	}
	msg := err.Error()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		msg = fmt.Sprintf("no answer in %s", CheckTimeout)
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		msg = fmt.Sprintf("exit %d", exit.ExitCode())
	}
	if line := lastLine(out.String()); line != "" {
		msg += ": " + line
	}
	return errors.New(msg)
}

// lastLine is the last non-empty line of a command's output, redacted and cut to an excerpt.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := inbox.Scrub(inbox.Clean(strings.TrimSpace(lines[len(lines)-1])))
	if len(l) > 200 {
		l = strings.ToValidUTF8(l[:200], "") + "…"
	}
	return l
}

// expiredCredential re-runs every credential's check after a step failed, and returns the first
// that fails, by name, with why. A step that failed while one fails is blamed on the access, not
// the agent: kind credential (CLAUDE.md §6.1). Run outside the run.json lock: a check takes a
// while.
func (e *Engine) expiredCredential(ctx context.Context, r *state.Run) (string, error) {
	for _, name := range slices.Sorted(maps.Keys(r.Spec.Credentials)) {
		if err := CheckCredential(ctx, r.Repo, r.Spec.Credentials[name]); err != nil {
			return name, err
		}
	}
	return "", nil
}

// recheck turns a failure this tick recorded into kind credential when a credential's check
// fails now. Only failures access could explain: not a template that didn't render, a loop
// guard, an abort, or a secret already missing.
func (e *Engine) recheck(ctx context.Context, r *state.Run) (string, bool) {
	f := r.Failure
	if f == nil || len(r.Spec.Credentials) == 0 {
		return "", false
	}
	switch f.Kind {
	case state.KindConfig, state.KindMaxHops, state.KindAborted, state.KindCredential:
		return "", false
	}
	name, err := e.expiredCredential(ctx, r)
	if name == "" || ctx.Err() != nil {
		return "", false
	}
	nf := *f
	nf.Kind = state.KindCredential
	nf.Reason = fmt.Sprintf("credential %s fails its check (%v); %s", name, err, f.Reason)
	e.redact(r, &nf)
	e.logf("✕ %s", nf.Reason)
	updated := false
	if _, uerr := state.Update(e.Home, r.ID, func(r *state.Run) error {
		if r.Failure == nil || r.Failure.Step != f.Step || r.Failure.Hop != f.Hop || r.Failure.Kind != f.Kind {
			return errors.New("not the same failure") // nothing to save
		}
		r.Failure.Kind, r.Failure.Reason = nf.Kind, nf.Reason
		if r.Status == state.Failed {
			r.Error = nf.Reason
		}
		updated = true
		return nil
	}); uerr != nil && !updated {
		return "", false
	}
	return nf.Reason, true
}
