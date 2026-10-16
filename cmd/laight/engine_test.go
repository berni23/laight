package main

import (
	"bytes"
	"encoding/json"
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

// The fake runtime is `sh`: each objective is a script, pasted into the seat like a prompt, that
// finishes the step with `$L done …` ($L is the laight binary, from the seat's env).
func fakeWorkflow(bin, body string) string {
	return strings.ReplaceAll(`version: 1
id: demo
env: { L: "BIN" }
`, "BIN", bin) + body
}

// waitEnded waits for the run to reach a final status and for its tmux session to go.
func (e *rigEnv) waitEnded(id string) *state.Run {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r, err := state.Load(e.home, id)
		if err == nil && !r.Live() && !e.srv.HasSession(r.Session) {
			return r
		}
		if time.Now().After(deadline) {
			screen, _ := e.srv.Capture(r.Session, "engine")
			evs, _ := state.Events(e.home, id)
			e.t.Fatalf("run still %s; events %+v\nengine:\n%s", r.Status, evs, screen)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stepsDone lists the run's step.done events as step:exit.
func (e *rigEnv) stepsDone(id string) []string {
	evs, _ := state.Events(e.home, id)
	var got []string
	for _, ev := range evs {
		if ev.Type == "step.done" {
			got = append(got, ev.Step+":"+ev.Data["exit"].(string))
		}
	}
	return got
}

func countEvents(evs []state.Event, typ, seat string) int {
	n := 0
	for _, ev := range evs {
		if ev.Type == typ && (seat == "" || ev.Seat == seat) {
			n++
		}
	}
	return n
}

// Series, outputs into templates, a fail → fix loop, a fresh seat, and the end of the run.
func TestEngineIssueCycleShape(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  impl: { runtime: shell, command: sh }
  rev: { runtime: shell, command: sh, cwd: repo, session: fresh }
steps:
  - id: implement
    seat: impl
    objective: '"$L" done handoff pr=42 title="a b"'
    next_hop: { on: { handoff: review } }
  - id: review
    seat: rev
    objective: '[ "{{ .steps.implement.out.pr }}/{{ .steps.implement.out.title }}" = "42/a b" ] && "$L" done handoff || "$L" done failed'
    next_hop: { on: { handoff: fix } }
  - id: fix
    seat: impl
    objective: '"$L" done handoff'
    next_hop: { on: { handoff: verify } }
  - id: verify
    seat: rev
    objective: 'if [ -e verified ]; then "$L" done pass; else touch verified; "$L" done fail; fi'
    allowed_exits: [pass, fail]
    next_hop: { on: { pass: end, fail: fix } }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Error)
	}
	want := "implement:handoff review:handoff fix:handoff verify:fail fix:handoff verify:pass"
	if got := strings.Join(e.stepsDone(r.ID), " "); got != want {
		t.Errorf("steps:\n got %s\nwant %s", got, want)
	}
	if r.Hops != 6 || r.Steps["implement"].Out["pr"] != json.Number("42") {
		t.Errorf("hops %d, implement %+v", r.Hops, r.Steps["implement"])
	}
	evs, _ := state.Events(e.home, r.ID)
	// rev is fresh: its CLI restarts before each of its steps after the first (3 steps, 3 starts);
	// impl resumes (1 start).
	if n, m := countEvents(evs, "seat.up", "rev"), countEvents(evs, "seat.up", "impl"); n != 3 || m != 1 {
		t.Errorf("seat.up: rev %d, impl %d", n, m)
	}
	if countEvents(evs, "step.started", "") != 6 || countEvents(evs, "run.finished", "") != 1 {
		t.Errorf("events: %+v", evs)
	}
	// on_finish.success defaults to destroy.
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "worktrees", "impl")); !os.IsNotExist(err) {
		t.Errorf("worktree kept after success: %v", err)
	}
}

func TestEngineMaxHops(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: ping, seat: a, objective: '"$L" done handoff', next_hop: { on: { handoff: pong } } }
  - { id: pong, seat: a, objective: '"$L" done handoff', next_hop: { on: { handoff: ping } } }
loop_guards: { max_hops: 3 }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Failed || !strings.Contains(r.Error, "max_hops (3) reached before step pong") {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	if got := len(e.stepsDone(r.ID)); got != 3 {
		t.Errorf("%d steps done, want 3", got)
	}
}

func TestEngineUnroutedFailed(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: '"$L" done failed error=boom', next_hop: { on: { handoff: two } } }
  - { id: two, seat: a, objective: '"$L" done handoff' }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Failed || r.Error != "step one failed: boom" {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	if got := strings.Join(e.stepsDone(r.ID), " "); got != "one:failed" {
		t.Errorf("steps %s", got)
	}
}

// A routed failed is a normal exit, and a seat that dies mid-step fails its step.
func TestEngineRoutedFailedAndCrash(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
  b: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: exit, next_hop: { on: { failed: two } } }
  - { id: two, seat: b, objective: '"$L" done handoff' }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Error)
	}
	if got := strings.Join(e.stepsDone(r.ID), " "); got != "one:failed two:handoff" {
		t.Errorf("steps %s", got)
	}
	if r.Steps["one"].Out["error"] != "seat a crashed" {
		t.Errorf("one: %+v", r.Steps["one"])
	}
}

// A seat whose agent exits between its steps is relaunched for the next one, not handed a dead pane.
func TestEngineRelaunchesIdleCrash(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
  b: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: '"$L" done handoff; exit', next_hop: { on: { handoff: two } } }
  - { id: two, seat: b, objective: 'sleep 1; "$L" done handoff', next_hop: { on: { handoff: three } } }
  - { id: three, seat: a, objective: '"$L" done handoff' }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Error)
	}
	evs, _ := state.Events(e.home, r.ID)
	if n, m := countEvents(evs, "seat.up", "a"), countEvents(evs, "seat.crashed", "a"); n != 2 || m != 1 {
		t.Errorf("seat a: %d seat.up, %d seat.crashed", n, m)
	}
}

// An objective that can't render fails its step instead of reaching the agent half-written.
func TestEngineRenderFailure(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: '"$L" done failed', next_hop: { on: { failed: two } } }
  - { id: two, seat: a, objective: 'echo {{ .steps.one.out.missing }}' }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Failed || !strings.Contains(r.Error, "step two failed: objective:") {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
}

// Shell steps: outputs into run:, a mapped exit code, a routed failure, the step's env with a
// secret kept out of its log, run in the repo and not as a seat.
func TestEngineShellSteps(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_TOK", "hunter2-xyz")
	t.Setenv("LAIGHT_SEAT", "a") // inherited by the engine: a shell step must not be seat a
	e := newRigEnv(t, "")
	writeWorkflow(t, e, strings.ReplaceAll(fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - id: one
    seat: a
    objective: '"$L" done handoff n=3'
    next_hop: { on: { handoff: check } }
  - id: check
    run: 'echo "token=$TOK greeting=$HELLO"; exit {{ .steps.one.out.n }}'
    env: { TOK: '{{ secret "tok" }}', HELLO: hi }
    exits: { "3": retry }
    next_hop: { on: { retry: boom, ok: end } }
  - id: boom
    run: 'echo oops >&2; exit 1'
    next_hop: { on: { failed: last } }
  - id: last
    run: '[ "$(pwd -P)" = "$(cd REPO && pwd -P)" ] && [ -z "$LAIGHT_SEAT" ] && [ "$LAIGHT_STEP" = last ]'
`), "REPO", e.repo))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Error)
	}
	if got := strings.Join(e.stepsDone(r.ID), " "); got != "one:handoff check:retry boom:failed last:ok" {
		t.Errorf("steps %s", got)
	}
	if r.Steps["check"].Out["code"] != json.Number("3") || !strings.Contains(r.Steps["boom"].Out["error"].(string), "exit code 1") {
		t.Errorf("check %+v, boom %+v", r.Steps["check"], r.Steps["boom"])
	}
	logs := map[string]string{}
	for _, f := range []string{"check-2", "boom-3"} {
		b, err := os.ReadFile(filepath.Join(state.Dir(e.home, r.ID), "out", f+".log"))
		if err != nil {
			t.Fatal(err)
		}
		logs[f] = string(b)
	}
	if want := "$ echo \"token=$TOK greeting=$HELLO\"; exit 3\ntoken=*** greeting=hi\n"; logs["check-2"] != want {
		t.Errorf("check log:\n%s\nwant:\n%s", logs["check-2"], want)
	}
	if !strings.Contains(logs["boom-3"], "oops") {
		t.Errorf("boom log: %s", logs["boom-3"])
	}
	evs, _ := os.ReadFile(filepath.Join(state.Dir(e.home, r.ID), "events.jsonl"))
	if strings.Contains(string(evs), "hunter2") {
		t.Errorf("secret in events.jsonl")
	}
}

// A shell step that runs before anything else (its run folder has no out/ yet), and an abort
// that stops it together with what it started.
func TestEngineShellFirstAndAbort(t *testing.T) {
	e := newRigEnv(t, "")
	pids := filepath.Join(t.TempDir(), "pids")
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: wait
    run: 'sleep 300 & echo $! > PIDS; sleep 301'
`, "PIDS", pids))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitFor(r.ID, "step.started", 1)
	deadline := time.Now().Add(10 * time.Second)
	var pid int
	for pid == 0 {
		b, _ := os.ReadFile(pids)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if pid == 0 && time.Now().After(deadline) {
			b, _ := os.ReadFile(filepath.Join(state.Dir(e.home, r.ID), "out", "wait-1.json"))
			t.Fatalf("the command never started; out: %s", b)
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.mustLaight("abort", r.Short(), "--yes")
	if r := e.waitEnded(r.ID); r.Status != state.Aborted {
		t.Errorf("status %s: %s", r.Status, r.Error)
	}
	for deadline := time.Now().Add(10 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the command's child %d outlived the run", pid)
		}
	}
}

func TestEngineUnsupported(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: x, next_hop: { on: { handoff: two } } }
  - { id: two, type: gate, ask: "ok?" }
`)
	// Refused before a run or a tmux session exists.
	if out, err := e.laight("run", "demo"); err == nil || !strings.Contains(out, "gates are not supported yet") {
		t.Errorf("run = %v\n%s", err, out)
	}
	if runs, _ := state.List(e.home); len(runs) != 0 {
		t.Errorf("an unsupported workflow left %d runs", len(runs))
	}
}

func writeWorkflow(t *testing.T, e *rigEnv, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.repo, ".laight", "workflows", "demo.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// doneEnv is a run.json with one step running on seat a, and the env `laight done` reads.
func doneEnv(t *testing.T) (home, id string) {
	t.Helper()
	home, id = t.TempDir(), state.NewID(time.Now())
	w := &spec.Workflow{ID: "wf", Steps: []*spec.Step{{ID: "verify", Seat: "a", AllowedExits: []string{"pass", "fail"}}}}
	r := &state.Run{
		ID: id, Workflow: "wf", Spec: w, Status: state.Running,
		Seats:  map[string]*state.Seat{"a": {Window: "a", Step: "verify", Hop: 3, Turn: true}},
		Active: []*state.Hop{{Step: "verify", Seat: "a", N: 3}},
	}
	if err := state.Create(home, r); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAIGHT_HOME", home)
	t.Setenv("LAIGHT_RUN", id)
	t.Setenv("LAIGHT_SEAT", "a")
	return home, id
}

func TestDone(t *testing.T) {
	home, id := doneEnv(t)
	for _, tt := range []struct {
		args []string
		code int
		want string
	}{
		{nil, 1, "usage: laight done"},
		{[]string{"handoff"}, 1, `step verify can't finish with "handoff"; its exits: pass, fail, failed`},
		{[]string{"pass", "novalue"}, 1, `outputs are key=value, got "novalue"`},
		{[]string{"pass", "n=7", "big=12345678901234567890", "nil=null", "list=[1,2]", "s=hello world", "url=https://x/1", "two=1 2"}, 0, "step verify done: pass"},
		{[]string{"fail"}, 1, "step verify already finished with pass"},
	} {
		var out, errOut bytes.Buffer
		if code := run(append([]string{"done"}, tt.args...), &out, &errOut); code != tt.code || !strings.Contains(out.String()+errOut.String(), tt.want) {
			t.Errorf("done %v = %d %q %q, want %d %q", tt.args, code, out.String(), errOut.String(), tt.code, tt.want)
		}
	}
	d, err := state.ReadDone(home, id, "verify", 3)
	if err != nil || d == nil {
		t.Fatalf("ReadDone: %v %v", d, err)
	}
	b, _ := json.Marshal(d.Out)
	if string(b) != `{"big":12345678901234567890,"list":[1,2],"n":7,"nil":"","s":"hello world","two":"1 2","url":"https://x/1"}` {
		t.Errorf("out = %s", b)
	}
	// What a later objective sees: numbers digit for digit, null as nothing.
	r := &state.Run{Spec: &spec.Workflow{}, Steps: map[string]*state.StepResult{"verify": {Exit: d.Exit, Out: d.Out}}}
	if got, err := spec.Render("t", "{{ .steps.verify.out.big }}/{{ .steps.verify.out.nil }}/", r.TemplateData()); err != nil || got != "12345678901234567890//" {
		t.Errorf("rendered %q, %v", got, err)
	}

	t.Setenv("LAIGHT_SEAT", "b")
	var errOut bytes.Buffer
	if code := run([]string{"done", "pass"}, &bytes.Buffer{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "seat b has no step running") {
		t.Errorf("done on an idle seat: %d %s", code, errOut.String())
	}
	t.Setenv("LAIGHT_RUN", "")
	errOut.Reset()
	if code := run([]string{"done", "pass"}, &bytes.Buffer{}, &errOut); code != 1 || !strings.Contains(errOut.String(), "LAIGHT_RUN and LAIGHT_SEAT are not set") {
		t.Errorf("done outside a run: %d %s", code, errOut.String())
	}
}

// The Stop hook: block once with how to finish, never twice; once done, mark the seat idle.
func TestStopHook(t *testing.T) {
	home, id := doneEnv(t)
	idle := filepath.Join(state.Dir(home, id), "seats", "a.idle")
	hook := func(input string) string {
		var out, errOut bytes.Buffer
		if code := cmdHook([]string{"stop"}, strings.NewReader(input), &out, &errOut); code != 0 || errOut.Len() > 0 {
			t.Fatalf("hook: %d %s", code, errOut.String())
		}
		return out.String()
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(hook(`{}`)), &got); err != nil || got["decision"] != "block" ||
		!strings.Contains(got["reason"], " done <exit> [key=value…] (exits: pass, fail, failed)") {
		t.Errorf("first stop: %v %v", got, err)
	}
	if out := hook(`{"stop_hook_active": true}`); out != "" {
		t.Errorf("second stop blocked again: %s", out)
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Errorf("idle before done: %v", err)
	}
	if err := state.WriteDone(home, id, state.Done{Step: "verify", Hop: 3, Exit: "pass"}); err != nil {
		t.Fatal(err)
	}
	if out := hook(`{}`); out != "" {
		t.Errorf("stop after done: %s", out)
	}
	if b, _ := os.ReadFile(idle); string(b) != "verify-3\n" {
		t.Errorf("idle = %q", b)
	}
}
