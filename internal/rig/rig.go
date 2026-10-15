// Package rig spins up and tears down the agents of one run: their git worktrees and their tmux
// windows. A rig lives exactly as long as its run.
package rig

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/berni23/laight/internal/runtime"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// EngineWindow is window 0 of every rig: the engine.
const EngineWindow = "engine"

// ReadyTimeout bounds how long a seat may take to start.
var ReadyTimeout = 90 * time.Second

// Rig drives the runs of one laight home.
type Rig struct {
	Home   string
	Laight string // absolute path of the laight binary, for hooks and `laight done`
	Tmux   tmux.Server
	Log    io.Writer // progress lines, shown in the engine window
}

// New returns the rig driver for home.
func New(home, laight string, log io.Writer) *Rig {
	return &Rig{Home: home, Laight: laight, Tmux: tmux.Server{Socket: state.Socket(home)}, Log: log}
}

func (g *Rig) logf(format string, args ...any) {
	if g.Log != nil {
		fmt.Fprintf(g.Log, format+"\n", args...)
	}
}

func (g *Rig) event(id, typ, seat string, data map[string]any) {
	if err := state.Append(g.Home, state.Event{Run: id, Type: typ, Seat: seat, Data: data}); err != nil {
		g.logf("warning: can't write events.jsonl: %v", err)
	}
}

// Up starts every seat of the run that isn't up yet: worktree, setup, window, ready.
func (g *Rig) Up(ctx context.Context, id string) error {
	r, err := state.Load(g.Home, id)
	if err != nil {
		return err
	}
	if needsWorktree(r.Spec) && r.Base == "" {
		ref, err := baseRef(ctx, r.Repo, r.Spec.Base, g.logf)
		if err != nil {
			return err
		}
		if r, err = state.Update(g.Home, id, func(r *state.Run) error { r.Base = ref; return nil }); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(r.Spec.Seats))
	for n := range r.Spec.Seats {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if s := r.Seats[name]; s != nil && s.Up && g.Tmux.HasWindow(r.Session, s.Window) {
			continue
		}
		if err := g.seatUp(ctx, r, name); err != nil {
			return fmt.Errorf("seat %s: %w", name, err)
		}
	}
	return nil
}

func needsWorktree(w *spec.Workflow) bool {
	for _, s := range w.Seats {
		if s.Cwd == "worktree" {
			return true
		}
	}
	return false
}

func (g *Rig) seatUp(ctx context.Context, r *state.Run, name string) error {
	seat := r.Spec.Seats[name]
	rt, err := runtime.For(seat.Runtime)
	if err != nil {
		return err
	}
	runDir := state.Dir(g.Home, r.ID)

	// Where the agent works.
	st := &state.Seat{Window: name}
	save := func() error {
		_, err := state.Update(g.Home, r.ID, func(r *state.Run) error {
			if r.Seats == nil {
				r.Seats = map[string]*state.Seat{}
			}
			r.Seats[name] = st
			return nil
		})
		return err
	}
	switch seat.Cwd {
	case "worktree":
		st.Dir, st.Worktree = filepath.Join(runDir, "worktrees", name), true
		if _, err := os.Stat(st.Dir); errors.Is(err, os.ErrNotExist) {
			g.logf("%s: worktree at %s (%s)", name, st.Dir, r.Base)
			if err := git(ctx, r.Repo, "worktree", "add", "--detach", st.Dir, r.Base); err != nil {
				return err
			}
			// Recorded before setup, so a failing setup still leaves it to on_finish.
			if err := save(); err != nil {
				return err
			}
			if seat.Setup != "" {
				g.logf("%s: setup: %s", name, seat.Setup)
				if err := setup(ctx, st.Dir, seat.Setup, filepath.Join(runDir, "seats", name+".setup.log")); err != nil {
					return err
				}
			}
		}
	case "repo":
		st.Dir = r.Repo
	default:
		st.Dir = seat.Cwd
		if !filepath.IsAbs(st.Dir) {
			st.Dir = filepath.Join(r.Repo, st.Dir)
		}
		if fi, err := os.Stat(st.Dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("cwd %s is not a folder", st.Dir)
		}
	}

	// How it starts.
	env, err := seatEnv(r, seat)
	if err != nil {
		return err
	}
	env["LAIGHT_RUN"], env["LAIGHT_SEAT"], env["LAIGHT_HOME"] = r.ID, name, g.Home
	l := runtime.Launch{Name: r.Short() + "/" + name, Seat: seat, SessionID: uuid()}
	if seat.Guidance != "" {
		l.Guidance = filepath.Join(r.SpecDir, filepath.FromSlash(seat.Guidance))
	}
	if b := rt.Settings(g.Laight); b != nil {
		l.Settings = filepath.Join(runDir, "seats", name+".settings.json")
		if err := os.WriteFile(l.Settings, b, 0o600); err != nil {
			return err
		}
	}
	st.SessionID = l.SessionID
	ready := filepath.Join(runDir, "seats", name+".ready")
	os.Remove(ready)

	g.Tmux.KillWindow(r.Session, name) // a dead pane left from a crash
	if err := g.Tmux.NewWindow(r.Session, tmux.Window{Name: name, Dir: st.Dir, Env: env, Cmd: rt.Command(l)}); err != nil {
		return err
	}
	if err := g.Tmux.PipeTo(r.Session, name, filepath.Join(runDir, "seats", name+".log")); err != nil {
		return err
	}
	if err := save(); err != nil {
		return err
	}
	if err := g.waitReady(ctx, r.Session, name, rt, ready, st.Worktree); err != nil {
		return err
	}
	st.Up = true
	if err := save(); err != nil {
		return err
	}
	g.event(r.ID, "seat.up", name, map[string]any{"dir": st.Dir, "runtime": seat.Runtime})
	g.logf("%s: up (%s)", name, seat.Runtime)
	return nil
}

// waitReady waits for the SessionStart hook (runtimes with hooks) and answers the launch prompts
// the runtime allows, but only in a worktree laight created: elsewhere trusting a folder is the
// user's call.
func (g *Rig) waitReady(ctx context.Context, session, window string, rt runtime.Runtime, readyFile string, ownDir bool) error {
	deadline := time.Now().Add(ReadyTimeout)
	for {
		if g.Tmux.PaneDead(session, window) {
			screen, _ := g.Tmux.Capture(session, window)
			return fmt.Errorf("exited while starting:\n%s", tail(screen, 15))
		}
		if !rt.Hooks() {
			return nil
		}
		if _, err := os.Stat(readyFile); err == nil {
			return nil
		}
		screen, err := g.Tmux.Capture(session, window)
		if err != nil {
			return err
		}
		if key := runtime.Choose(screen, rt.Accept()); key != "" {
			if !ownDir {
				return fmt.Errorf("waiting on a launch prompt; laight only answers it in its own worktrees, so answer it once with `laight attach`:\n%s", tail(screen, 15))
			}
			if err := g.Tmux.SendKeys(session, window, key); err != nil {
				return err
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready after %s:\n%s", ReadyTimeout, tail(screen, 15))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// seatEnv renders the workflow's and the seat's env (the seat's wins).
func seatEnv(r *state.Run, seat *spec.Seat) (map[string]string, error) {
	inputs := map[string]any{}
	for k, v := range r.Inputs {
		inputs[k] = v
	}
	data := map[string]any{
		"inputs": inputs, "base": r.Spec.Base, "steps": map[string]any{}, "matrix": map[string]any{},
		"run": map[string]any{"id": r.ID, "short": r.Short()},
	}
	env := map[string]string{}
	for _, m := range []spec.StringMap{r.Spec.Env, seat.Env} {
		for k, v := range m {
			out, err := spec.Render("env "+k, v, data)
			if err != nil {
				return nil, fmt.Errorf("env %s: %w", k, err)
			}
			env[k] = out
		}
	}
	return env, nil
}

// Down ends the run: it stops the seats, applies on_finish to the worktrees, records the final
// status and stops the tmux session. status is Succeeded, Failed or Aborted. killFirst stops the
// whole session before anything else, which `laight abort` does so the engine can't race it;
// the engine itself passes false and dies last, with the session.
func (g *Rig) Down(id, status, reason string, killFirst bool) error {
	r, err := state.Load(g.Home, id)
	if err != nil {
		return err
	}
	if killFirst {
		if err := g.Tmux.KillSession(r.Session); err != nil {
			return err
		}
	} else {
		for name, s := range r.Seats {
			if err := g.Tmux.KillWindow(r.Session, s.Window); err != nil {
				g.logf("%s: %v", name, err)
			}
		}
	}
	policy := r.Spec.OnFinish.Failure
	if status == state.Succeeded {
		policy = r.Spec.OnFinish.Success
	}
	var errs []error
	removed := []string{}
	if policy == "destroy" {
		removed, errs = g.RemoveWorktrees(id)
	}
	now := time.Now().UTC()
	if _, err := state.Update(g.Home, id, func(r *state.Run) error {
		r.Status, r.Error, r.Finished = status, reason, &now
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	typ := "run.finished"
	if status == state.Aborted {
		typ = "run.aborted"
	}
	data := map[string]any{"status": status, "worktrees": policy, "removed": len(removed)}
	if reason != "" {
		data["error"] = reason
	}
	g.event(id, typ, "", data)
	if err := g.Tmux.KillSession(r.Session); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Worktrees returns the run's worktrees that still exist.
func (g *Rig) Worktrees(r *state.Run) []string {
	var dirs []string
	for _, s := range r.Seats {
		if s.Worktree {
			if _, err := os.Stat(s.Dir); err == nil {
				dirs = append(dirs, s.Dir)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

// RemoveWorktrees deletes the run's worktrees, uncommitted changes included, and returns the ones
// it removed.
func (g *Rig) RemoveWorktrees(id string) ([]string, []error) {
	r, err := state.Load(g.Home, id)
	if err != nil {
		return nil, []error{err}
	}
	var removed []string
	var errs []error
	ctx := context.Background()
	for _, dir := range g.Worktrees(r) {
		if err := git(ctx, r.Repo, "worktree", "remove", "--force", dir); err != nil {
			// The repo moved or the worktree is broken: delete the folder, then let git forget it.
			if err := os.RemoveAll(dir); err != nil {
				errs = append(errs, err)
				continue
			}
			git(ctx, r.Repo, "worktree", "prune")
		}
		removed = append(removed, dir)
		g.logf("removed worktree %s", dir)
	}
	return removed, errs
}

// baseRef is the ref worktrees start from: origin/<base> after a fetch, else the local branch.
func baseRef(ctx context.Context, repo, base string, logf func(string, ...any)) (string, error) {
	if git(ctx, repo, "remote", "get-url", "origin") == nil {
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := git(fctx, repo, "fetch", "--quiet", "origin", base)
		cancel()
		if err != nil {
			logf("warning: can't fetch origin/%s, using what's local: %v", base, err)
		}
		if git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+base) == nil {
			return "origin/" + base, nil
		}
	}
	if git(ctx, repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+base) == nil {
		return base, nil
	}
	return "", fmt.Errorf("base branch %q not found locally or on origin (set `base:` in the workflow)", base)
}

func git(ctx context.Context, repo string, args ...string) error {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", args[0], msg)
	}
	return nil
}

func setup(ctx context.Context, dir, cmd, logFile string) error {
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir, c.Stdout, c.Stderr = dir, f, f
	if err := c.Run(); err != nil {
		b, _ := os.ReadFile(logFile)
		return fmt.Errorf("setup %q failed (%v):\n%s", cmd, err, tail(string(b), 15))
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	var keep []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, "\n")
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
