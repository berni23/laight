package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
	"golang.org/x/term"
)

// cmdRun validates a workflow, creates the run and starts its engine in a new tmux session,
// then follows it when there's a terminal to follow it in.
func cmdRun(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail(stderr, "usage: laight run <workflow> [key=value…]")
	}
	p, err := spec.FindProject(".")
	if err != nil {
		return fail(stderr, "%v", err)
	}
	file, err := p.WorkflowFile(args[0])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	w, probs := spec.Load(p.Dir, file)
	for _, pr := range probs {
		fmt.Fprintln(stderr, pr)
	}
	if w == nil || spec.HasErrors(probs) {
		return fail(stderr, "fix the workflow first (`laight validate %s`)", args[0])
	}
	inputs, err := parseInputs(w, args[1:])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if _, err := tmux.Version(); err != nil {
		return fail(stderr, "%v", err)
	}
	home, laight, err := homeAndBinary()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if err := state.Register(home, p.Root); err != nil {
		return fail(stderr, "%v", err)
	}

	id := state.NewID(time.Now())
	r := &state.Run{
		ID: id, Workflow: w.ID, Repo: p.Root, Session: state.SessionName(w.ID, id),
		Inputs: inputs, Spec: w, SpecDir: p.Dir, Status: state.Starting,
		Seats: map[string]*state.Seat{}, Created: time.Now().UTC(),
	}
	if err := state.Create(home, r); err != nil {
		return fail(stderr, "%v", err)
	}
	srv := tmux.Server{Socket: state.Socket(home)}
	err = srv.NewSession(r.Session, tmux.Window{
		Name: rig.EngineWindow, Dir: p.Root,
		Env: map[string]string{"LAIGHT_HOME": home},
		Cmd: tmux.Join([]string{laight, "engine", id}),
	})
	if err != nil {
		state.Update(home, id, func(r *state.Run) error { r.Status, r.Error = state.Failed, err.Error(); return nil })
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stdout, "run %s: %s started (tmux session %s)\n", r.Short(), w.ID, r.Session)
	if !interactive() {
		fmt.Fprintf(stdout, "follow it: laight attach %s\n", r.Short())
		return 0
	}
	return follow(srv, home, r, "", stdout, stderr)
}

// parseInputs turns key=value arguments into the run's inputs, with defaults, and checks them.
func parseInputs(w *spec.Workflow, args []string) (map[string]string, error) {
	in := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("inputs are key=value, got %q", a)
		}
		if _, known := w.Inputs[k]; !known {
			return nil, fmt.Errorf("unknown input %q (inputs: %s)", k, strings.Join(sortedKeys(w.Inputs), ", "))
		}
		in[k] = v
	}
	var missing []string
	for _, k := range sortedKeys(w.Inputs) {
		def := w.Inputs[k]
		if _, set := in[k]; set {
			continue
		}
		switch {
		case def.Default != nil:
			in[k] = fmt.Sprint(def.Default)
		case def.Required:
			missing = append(missing, k+"=…")
		default:
			in[k] = ""
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required input: laight run %s %s", w.ID, strings.Join(missing, " "))
	}
	return in, nil
}

// cmdEngine is window 0 of a rig. In this version it brings the seats up and holds the rig until
// `laight abort`; steps and routing come with the engine loop (M2).
func cmdEngine(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		return fail(stderr, "usage: laight engine <run id>")
	}
	home, laight, err := homeAndBinary()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	id := args[0]
	r, err := state.Load(home, id)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if !r.Live() {
		return fail(stderr, "run %s is %s", r.Short(), r.Status)
	}
	g := rig.New(home, laight, stdout)
	fmt.Fprintf(stdout, "laight %s · run %s · %s · %s\n\n", version, r.Short(), r.Workflow, r.Repo)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if r.Status == state.Starting {
		state.Update(home, id, func(r *state.Run) error { r.Status = state.Running; return nil })
		state.Append(home, state.Event{Run: id, Type: "run.started", Data: map[string]any{"workflow": r.Workflow, "repo": r.Repo}})
	}
	if err := g.Up(ctx, id); err != nil {
		if ctx.Err() != nil {
			return 1 // stopped from outside (abort, Ctrl-C): the run stays as it is
		}
		fmt.Fprintf(stdout, "\n✕ %v\n", err)
		reason, _, _ := strings.Cut(err.Error(), "\n") // the screen excerpt stays out of run.json
		if err := g.Down(id, state.Failed, reason, false); err != nil {
			fmt.Fprintf(stdout, "teardown: %v\n", err)
		}
		return 1
	}

	r, _ = state.Load(home, id)
	fmt.Fprintf(stdout, "\n✓ rig up: %d seats\n", len(r.Seats))
	for _, name := range sortedKeys(r.Seats) {
		fmt.Fprintf(stdout, "  %-12s %s\n", name, r.Seats[name].Dir)
	}
	fmt.Fprintf(stdout, "\nThis version starts the rig only; steps run with the engine loop (M2).\n")
	fmt.Fprintf(stdout, "Switch windows with your tmux prefix + w. Stop the rig with: laight abort %s\n", r.Short())
	<-ctx.Done()
	return 0
}

// cmdHook handles the runtime's hooks. It must never get in the agent's way: any problem is
// reported on stderr and the exit code is 0.
func cmdHook(args []string, stdin io.Reader, stderr io.Writer) int {
	runID, seat := os.Getenv("LAIGHT_RUN"), os.Getenv("LAIGHT_SEAT")
	if len(args) != 1 || runID == "" || seat == "" {
		return 0 // not a laight seat
	}
	home, err := state.Home()
	if err != nil {
		fmt.Fprintf(stderr, "laight hook: %v\n", err)
		return 0
	}
	var in struct {
		SessionID string `json:"session_id"`
		Source    string `json:"source"`
	}
	json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in)
	switch args[0] {
	case "session-start":
		b, _ := json.Marshal(map[string]any{"session_id": in.SessionID, "source": in.Source, "ts": time.Now().UTC()})
		if err := os.WriteFile(filepath.Join(state.Dir(home, runID), "seats", seat+".ready"), b, 0o600); err != nil {
			fmt.Fprintf(stderr, "laight hook: %v\n", err)
		}
	}
	state.Append(home, state.Event{Run: runID, Seat: seat, Type: "hook." + args[0], Data: map[string]any{"source": in.Source}})
	return 0
}

// cmdAttach puts the terminal in a run's tmux session, at the engine or at one seat.
func cmdAttach(args []string, stdout, stderr io.Writer) int {
	if len(args) > 2 {
		return fail(stderr, "usage: laight attach [run] [seat]")
	}
	home, srv, r, err := findRun(args, func(r *state.Run) bool { return r.Live() })
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if !srv.HasSession(r.Session) {
		return fail(stderr, "run %s is %s and its tmux session is gone", r.Short(), r.Status)
	}
	window := ""
	if len(args) == 2 {
		window = args[1]
		if !srv.HasWindow(r.Session, window) {
			ws, _ := srv.Windows(r.Session)
			return fail(stderr, "run %s has no seat %q (windows: %s)", r.Short(), window, strings.Join(ws, ", "))
		}
	}
	if !interactive() {
		return fail(stderr, "attach needs a terminal; or: tmux -L %s attach -t %s", srv.Socket, r.Session)
	}
	return follow(srv, home, r, window, stdout, stderr)
}

// follow attaches to the run and, once the terminal is back, says where the run stands.
func follow(srv tmux.Server, home string, r *state.Run, window string, stdout, stderr io.Writer) int {
	if window == "" {
		window = rig.EngineWindow
	}
	srv.SelectWindow(r.Session, window)
	if err := srv.AttachCmd(r.Session, "").Run(); err != nil {
		return fail(stderr, "attach: %v", err)
	}
	r, err := state.Load(home, r.ID)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	switch {
	case r.Live():
		fmt.Fprintf(stdout, "run %s is %s; back with: laight attach %s\n", r.Short(), r.Status, r.Short())
	case r.Error != "":
		fmt.Fprintf(stdout, "run %s %s: %s\n", r.Short(), r.Status, r.Error)
		return 1
	default:
		fmt.Fprintf(stdout, "run %s %s\n", r.Short(), r.Status)
	}
	return 0
}

// cmdAbort stops a live run, or removes the worktrees a finished run kept.
func cmdAbort(args []string, stdout, stderr io.Writer) int {
	args, yes := flagYes(args)
	if len(args) > 1 {
		return fail(stderr, "usage: laight abort [run] [--yes]")
	}
	home, srv, r, err := findRun(args, func(r *state.Run) bool { return r.Live() })
	if err != nil {
		return fail(stderr, "%v", err)
	}
	laight, _ := os.Executable()
	g := rig.New(home, laight, stdout)
	worktrees := g.Worktrees(r)

	if r.Live() {
		plan := []string{}
		if srv.HasSession(r.Session) {
			ws, _ := srv.Windows(r.Session)
			plan = append(plan, fmt.Sprintf("stop tmux session %s (%s)", r.Session, strings.Join(ws, ", ")))
		}
		if r.Spec.OnFinish.Failure == "destroy" {
			for _, wt := range worktrees {
				plan = append(plan, "delete worktree "+wt+", with any uncommitted changes")
			}
		} else if len(worktrees) > 0 {
			plan = append(plan, fmt.Sprintf("keep %d worktree(s) (on_finish.failure: keep); `laight abort %s` again deletes them", len(worktrees), r.Short()))
		}
		if !confirm(fmt.Sprintf("Abort run %s (%s, %s):", r.Short(), r.Workflow, r.Status), plan, yes, stdout, stderr) {
			return 1
		}
		if err := g.Down(r.ID, state.Aborted, "", true); err != nil {
			return fail(stderr, "%v", err)
		}
		fmt.Fprintf(stdout, "run %s aborted\n", r.Short())
		return 0
	}

	if len(worktrees) == 0 {
		fmt.Fprintf(stdout, "run %s is %s and has no worktrees left; nothing to do\n", r.Short(), r.Status)
		return 0
	}
	plan := make([]string, len(worktrees))
	for i, wt := range worktrees {
		plan[i] = "delete worktree " + wt + ", with any uncommitted changes"
	}
	if !confirm(fmt.Sprintf("Run %s (%s) is %s.", r.Short(), r.Workflow, r.Status), plan, yes, stdout, stderr) {
		return 1
	}
	removed, errs := g.RemoveWorktrees(r.ID)
	state.Append(home, state.Event{Run: r.ID, Type: "run.cleaned", Data: map[string]any{"removed": len(removed)}})
	if len(errs) > 0 {
		return fail(stderr, "%v", errors.Join(errs...))
	}
	return 0
}

// findRun resolves [run] for attach and abort: a short or full id prefix, or the project's only
// live run when none is given (that needs to be inside the project).
func findRun(args []string, live func(*state.Run) bool) (string, tmux.Server, *state.Run, error) {
	home, err := state.Home()
	if err != nil {
		return "", tmux.Server{}, nil, err
	}
	srv := tmux.Server{Socket: state.Socket(home)}
	ref, repo := "", ""
	if len(args) > 0 {
		ref = args[0]
	} else {
		p, err := spec.FindProject(".")
		if err != nil {
			return "", srv, nil, fmt.Errorf("name a run, or run this inside its project: %w", err)
		}
		repo = p.Root
	}
	r, err := state.Find(home, repo, ref, live)
	return home, srv, r, err
}

// confirm lists what will happen and asks; --yes skips the question. Without a terminal to ask
// in, it refuses rather than guessing.
func confirm(title string, plan []string, yes bool, stdout, stderr io.Writer) bool {
	fmt.Fprintln(stdout, title)
	for _, p := range plan {
		fmt.Fprintln(stdout, "  - "+p)
	}
	if yes {
		return true
	}
	if !stdinTerminal() {
		fmt.Fprintln(stderr, "laight: no terminal to confirm in; add --yes")
		return false
	}
	fmt.Fprint(stdout, "Go ahead? [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a == "y" || a == "yes" {
		return true
	}
	fmt.Fprintln(stdout, "nothing changed")
	return false
}

func flagYes(args []string) ([]string, bool) {
	var rest []string
	yes := false
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			yes = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, yes
}

// homeAndBinary returns laight's home and the absolute path of this binary, which the rig puts
// in hooks and settings.
func homeAndBinary() (string, string, error) {
	home, err := state.Home()
	if err != nil {
		return "", "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	return home, exe, nil
}

func stdinTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

func interactive() bool { return stdinTerminal() && term.IsTerminal(int(os.Stdout.Fd())) }
