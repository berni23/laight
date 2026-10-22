package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/berni23/laight/internal/engine"
	"github.com/berni23/laight/internal/inbox"
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

// waitItem waits for the run's next pending inbox item.
func (e *rigEnv) waitItem(id string) *inbox.Item {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		items, _ := inbox.Pending(e.home)
		for _, it := range items {
			if it.Run == id {
				return it
			}
		}
		if time.Now().After(deadline) {
			r, _ := state.Load(e.home, id)
			screen, _ := e.srv.Capture(r.Session, "engine")
			e.t.Fatalf("no inbox item; run %s %q\nengine:\n%s", r.Status, r.Error, screen)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// abortItem answers the run's failed item with abort, which fails the run.
func (e *rigEnv) abortItem(id string) {
	e.t.Helper()
	it := e.waitItem(id)
	if it.Kind != inbox.Failed {
		e.t.Fatalf("item %+v", it)
	}
	e.mustLaight("inbox", it.Short(), "abort")
}

// Agents learn `laight done` by name: the seat finds the binary running its rig on PATH, even
// when laight isn't installed anywhere (#11's first real run stopped on "command not found"),
// or is built under another name; nothing else from the binary's folder comes along (with brew,
// that folder is /opt/homebrew/bin, and its `claude` would shadow the user's).
func TestSeatFindsLaightOnPath(t *testing.T) {
	e := newRigEnv(t, "")
	dir := t.TempDir()
	b, err := os.ReadFile(e.bin)
	if err != nil {
		t.Fatal(err)
	}
	e.bin = filepath.Join(dir, "laight-dev")
	for name, data := range map[string][]byte{"laight-dev": b, "sibling": []byte("#!/bin/sh\n")} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: '! command -v sibling && [ "$(readlink "$(command -v laight)")" = "$L" ] && laight done handoff || "$L" done failed', next_hop: { on: { failed: end } } }
`))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if got := strings.Join(e.stepsDone(r.ID), " "); got != "one:handoff" {
		t.Errorf("steps %s; want one:handoff (status %s: %s)", got, r.Status, r.Error)
	}
}

// max_hops holds the run on a failed item: retry grants one more hop, abort fails the run.
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
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	if it.Kind != inbox.Failed || it.Step != "pong" || !strings.Contains(it.Title, "max_hops (3) reached before step pong") {
		t.Errorf("item %+v", it)
	}
	e.mustLaight("inbox", it.Short(), "retry")
	it = e.waitItem(r.ID) // pong ran, ping is refused next
	if it.Step != "ping" {
		t.Errorf("item %+v", it)
	}
	e.mustLaight("inbox", it.Short(), "abort")
	r = e.waitEnded(r.ID)
	if r.Status != state.Failed || !strings.Contains(r.Error, "max_hops (3) reached before step ping") {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	if got := len(e.stepsDone(r.ID)); got != 4 || r.Hops != 4 {
		t.Errorf("%d steps done, %d hops; want 4", got, r.Hops)
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
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	if it.Kind != inbox.Failed || it.Title != "step one failed: boom" || strings.Join(it.Choices, ",") != "retry,abort" {
		t.Errorf("item %+v", it)
	}
	if out, err := e.laight("inbox"); err != nil || !strings.Contains(out, it.Short()+"  failed") || !strings.Contains(out, "step one failed: boom  [retry|abort]") {
		t.Errorf("inbox: %v\n%s", err, out)
	}
	e.mustLaight("inbox", it.Short(), "abort")
	r = e.waitEnded(r.ID)
	if r.Status != state.Failed || r.Error != "step one failed: boom; aborted from the inbox" {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	if got := strings.Join(e.stepsDone(r.ID), " "); got != "one:failed" {
		t.Errorf("steps %s", got)
	}
	if out := e.mustLaight("inbox"); !strings.Contains(out, "inbox empty") {
		t.Errorf("inbox after the run:\n%s", out)
	}
}

// A failed step retried from the inbox starts again and the run goes on.
func TestEngineRetry(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, `version: 1
id: demo
steps:
  - { id: flaky, run: 'if [ -e tried ]; then exit 0; fi; touch tried; exit 1', next_hop: { on: { ok: last } } }
  - { id: last, run: 'rm tried' }
loop_guards: { max_hops: 2 }
`)
	// The retry is the third hop of two: it grants one more, so it isn't held again on the last step.
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	e.mustLaight("inbox", it.Short(), "retry")
	r = e.waitEnded(r.ID)
	if got := strings.Join(e.stepsDone(r.ID), " "); r.Status != state.Succeeded || got != "flaky:failed flaky:ok last:ok" {
		t.Errorf("status %s (%s), steps %s", r.Status, r.Error, got)
	}
	evs, _ := state.Events(e.home, r.ID)
	if countEvents(evs, "inbox.raised", "") != 1 || countEvents(evs, "inbox.answered", "") != 1 {
		t.Errorf("events %+v", evs)
	}
}

// An item whose run lost its engine can't be answered: nothing would ever read the answer.
func TestInboxEngineDied(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, `version: 1
id: demo
steps:
  - { id: approve, type: gate, ask: 'Go?' }
`)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	e.killEngine(r)
	if out := e.mustLaight("inbox"); !strings.Contains(out, "its engine died: laight abort "+r.Short()) || strings.Contains(out, "answer with") {
		t.Errorf("listed as answerable:\n%s", out)
	}
	if out, err := e.laight("inbox", it.Short(), "yes"); err == nil || !strings.Contains(out, "its engine died") {
		t.Errorf("answered for a dead engine: %v\n%s", err, out)
	}
	if done, _ := inbox.Answered(e.home, it.ID); done != nil {
		t.Errorf("answer recorded: %+v", done)
	}
}

// A gate asks in the inbox and routes on the answer; the first answer wins.
func TestEngineGate(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: build, seat: a, objective: '"$L" done handoff pr=42', next_hop: { on: { handoff: approve } } }
  - id: approve
    type: gate
    ask: Merge PR {{ .steps.build.out.pr }}?
    next_hop: { on: { yes: merge, no: end } }
  - { id: merge, run: 'true' }
`))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	if it.Kind != inbox.Gate || it.Title != "Merge PR 42?" || strings.Join(it.Choices, ",") != "yes,no" || it.Step != "approve" {
		t.Errorf("item %+v", it)
	}
	if out, err := e.laight("inbox", it.Short(), "maybe"); err == nil || !strings.Contains(out, "choose one of: yes, no") {
		t.Errorf("a wrong answer: %v\n%s", err, out)
	}
	if out := e.mustLaight("inbox", it.Short(), "yes"); !strings.Contains(out, it.Short()+": yes") {
		t.Errorf("answer: %s", out)
	}
	if out, err := e.laight("inbox", it.Short(), "no"); err == nil || !strings.Contains(out, "was already answered: yes") {
		t.Errorf("a second answer: %v\n%s", err, out)
	}
	r = e.waitEnded(r.ID)
	if got := strings.Join(e.stepsDone(r.ID), " "); r.Status != state.Succeeded || got != "build:handoff approve:yes merge:ok" {
		t.Errorf("status %s (%s), steps %s", r.Status, r.Error, got)
	}
}

// An abort withdraws the run's open items.
func TestEngineGateAbort(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
steps:
  - { id: approve, type: gate, ask: 'Go?', next_hop: { on: { yes: end, no: end } } }
`)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitItem(r.ID)
	e.mustLaight("abort", r.Short(), "--yes")
	e.waitEnded(r.ID)
	if items, _ := inbox.Pending(e.home); len(items) != 0 {
		t.Errorf("items left: %+v", items)
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
	e.abortItem(e.onlyRun().ID)
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Failed || !strings.Contains(r.Error, "step two failed: objective:") {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
}

// Shell steps: outputs into run:, a mapped exit code, a routed failure, the step's env with a
// secret kept out of its log, run in the repo and not as a seat.
func TestEngineShellSteps(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_TOK", "hunter2-xyz")
	t.Setenv("LAIGHT_SEAT", "a")                 // inherited by the engine: a shell step must not be seat a
	t.Setenv("LAIGHT_SECRET_OTHER", "other-xyz") // the engine's, but no step's env reads it
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
    run: '[ "$(pwd -P)" = "$(cd REPO && pwd -P)" ] && [ -z "$LAIGHT_SEAT" ] && [ "$LAIGHT_STEP" = last ] && [ -z "$LAIGHT_SECRET_OTHER" ] && [ -n "$LAIGHT_RUN" ]'
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
	waitGone(t, pid)
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "out", "wait-1.json")); !os.IsNotExist(err) {
		t.Errorf("a killed command was recorded as finished: %v", err)
	}
}

// waitGone fails the test unless process pid exits within 10s (and kills it if not).
func waitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("process %d outlived the run", pid)
		}
	}
}

// readPid waits for a command to write a pid to file.
func readPid(t *testing.T, file string) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(file)
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid != 0 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing wrote a pid to %s", file)
		}
	}
}

// A step that fails while a shell step still runs holds the run on an item; the command keeps
// running, and aborting from the inbox ends the run at once and stops it.
func TestEngineShellStoppedWhenRunFails(t *testing.T) {
	e := newRigEnv(t, "")
	pids := filepath.Join(t.TempDir(), "pids")
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: slow
    run: 'sleep 300 & echo $! > PIDS; wait'
  - id: bad
    run: 'sleep 1; exit 1'
`, "PIDS", pids))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	pid := readPid(t, pids)
	e.waitItem(r.ID)
	if syscall.Kill(pid, 0) != nil {
		t.Error("the sibling command was stopped by the failure")
	}
	start := time.Now()
	e.abortItem(r.ID)
	if r := e.waitEnded(r.ID); r.Status != state.Failed {
		t.Errorf("status %s: %s", r.Status, r.Error)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("the run took %s to end", d)
	}
	waitGone(t, pid)
}

// A command that exits 0 and leaves a child behind succeeded; the child is stopped with it.
func TestEngineShellBackgroundChild(t *testing.T) {
	e := newRigEnv(t, "")
	pids := filepath.Join(t.TempDir(), "pids")
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: spawn
    run: 'sleep 300 & echo $! > PIDS'
`, "PIDS", pids))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	pid := readPid(t, pids)
	r = e.waitEnded(r.ID)
	if r.Status != state.Succeeded || r.Steps["spawn"].Out["code"] != json.Number("0") {
		t.Errorf("status %s: %s; spawn %+v", r.Status, r.Error, r.Steps["spawn"])
	}
	waitGone(t, pid)
}

// An abort after the engine died still stops a running command and what it started.
func TestEngineShellAbortAfterEngineDied(t *testing.T) {
	e := newRigEnv(t, "")
	dir := t.TempDir()
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: wait
    run: 'echo $PPID > DIR/engine; sleep 300 & echo $! > DIR/pids; wait'
`, "DIR", dir))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	pid := readPid(t, filepath.Join(dir, "pids"))
	if err := syscall.Kill(readPid(t, filepath.Join(dir, "engine")), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if out := e.mustLaight("abort", r.Short(), "--yes"); !strings.Contains(out, "stop shell step wait (process group ") {
		t.Errorf("the plan doesn't say it stops the command:\n%s", out)
	}
	waitGone(t, pid)
}

// A command that wants a terminal fails at once instead of hanging the run.
func TestEngineShellNoTerminal(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
steps:
  - id: ask
    run: 'read x < /dev/tty'
`)
	e.mustLaight("run", "demo")
	e.abortItem(e.onlyRun().ID)
	if r := e.waitEnded(e.onlyRun().ID); r.Status != state.Failed {
		t.Errorf("status %s: %s", r.Status, r.Error)
	}
}

// A `run:` that doesn't render fails its step, which routes like any failure.
func TestEngineShellRenderFailure(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
steps:
  - id: one
    run: 'echo {{ .steps.one.out.nope }}'
    next_hop: { on: { failed: two } }
  - id: two
    run: 'true'
`)
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if got := strings.Join(e.stepsDone(r.ID), " "); r.Status != state.Succeeded || got != "one:failed two:ok" {
		t.Errorf("status %s (%s), steps %s", r.Status, r.Error, got)
	}
	if msg, _ := r.Steps["one"].Out["error"].(string); !strings.HasPrefix(msg, "run:") {
		t.Errorf("one: %+v", r.Steps["one"])
	}
}

// needsWorkflow is plan → build (seat a) and security (seat b) in parallel → integrate (seat a).
// build waits for a file security writes, so the two overlap whatever the timing. security fails
// once when the run is given fail=1.
const needsWorkflow = `seats:
  a: { runtime: shell, command: sh, cwd: repo }
  b: { runtime: shell, command: sh, cwd: repo }
inputs:
  fail: { required: false }
steps:
  - { id: plan, seat: a, objective: '"$L" done handoff' }
  - id: build
    seat: a
    needs: [plan]
    objective: 'while [ ! -e sec ]; do sleep 0.1; done; "$L" done handoff'
  - id: security
    seat: b
    needs: [plan]
    objective: 'touch sec; if [ "{{ .inputs.fail }}" = 1 ] && [ ! -e failed-once ]; then touch failed-once; "$L" done failed error=boom; else "$L" done handoff; fi'
  - id: integrate
    seat: a
    needs: [build, security]
    objective: '"$L" done handoff'
`

// stepEvents lists the run's step.started and step.done events as started:step and done:step.
func (e *rigEnv) stepEvents(id string) []string {
	evs, _ := state.Events(e.home, id)
	var got []string
	for _, ev := range evs {
		if typ, ok := strings.CutPrefix(ev.Type, "step."); ok && (typ == "started" || typ == "done") {
			got = append(got, typ+":"+ev.Step)
		}
	}
	return got
}

// Steps with needs run in parallel once their needs finish, and a join waits for every one.
func TestEngineNeeds(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, needsWorkflow))
	e.mustLaight("run", "demo")
	r := e.waitEnded(e.onlyRun().ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Error)
	}
	got := e.stepEvents(r.ID)
	at := func(ev string) int {
		i := slices.Index(got, ev)
		if i < 0 {
			t.Fatalf("no %s: %v", ev, got)
		}
		return i
	}
	if at("started:security") > at("done:build") || at("started:build") < at("done:plan") {
		t.Errorf("build and security didn't overlap after plan: %v", got)
	}
	if i := at("started:integrate"); i < at("done:build") || i < at("done:security") {
		t.Errorf("integrate didn't wait for both: %v", got)
	}
}

// A needed step that fails is held: its sibling finishes, the join waits, and a retry lets it run.
func TestEngineNeedsHeldThenRetry(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, needsWorkflow))
	e.mustLaight("run", "demo", "fail=1")
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	if it.Kind != inbox.Failed || it.Step != "security" {
		t.Fatalf("item %+v", it)
	}
	e.waitFor(r.ID, "step.done", 3) // plan, security, then build finishes too
	time.Sleep(3 * engine.Poll)     // a few ticks: integrate must still not start
	if got := e.stepEvents(r.ID); slices.Index(got, "done:build") < 0 || slices.Index(got, "started:integrate") >= 0 {
		t.Fatalf("while held: %v", got)
	}
	e.mustLaight("inbox", it.Short(), "retry")
	r = e.waitEnded(r.ID)
	// build and security:failed finish in either order.
	got := e.stepsDone(r.ID)
	if len(got) == 5 && got[1] == "build:handoff" {
		got[1], got[2] = got[2], got[1]
	}
	want := "plan:handoff security:failed build:handoff security:handoff integrate:handoff"
	if r.Status != state.Succeeded || strings.Join(got, " ") != want {
		t.Errorf("status %s (%s), steps:\n got %s\nwant %s", r.Status, r.Error, strings.Join(got, " "), want)
	}
}

// Aborting the held step fails the run, and the join never starts.
func TestEngineNeedsHeldThenAbort(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, needsWorkflow))
	e.mustLaight("run", "demo", "fail=1")
	r := e.onlyRun()
	e.abortItem(r.ID)
	r = e.waitEnded(r.ID)
	if r.Status != state.Failed || r.Error != "step security failed: boom; aborted from the inbox" {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	if got := e.stepEvents(r.ID); slices.Index(got, "started:integrate") >= 0 {
		t.Errorf("integrate started: %v", got)
	}
}

// The §3 example: plan lists the tasks, build runs one copy per task (two at a time) while
// security runs, and integrate sees every copy's output. Copy b fails once and is retried alone.
func TestEngineMatrix(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  lead: { runtime: shell, command: sh, cwd: repo }
  sec: { runtime: shell, command: sh, cwd: repo }
  dev: { runtime: shell, command: sh }
steps:
  - id: plan
    seat: lead
    objective: |
      "$L" done handoff 'tasks=["a","b","c"]'
  - id: build
    needs: [plan]
    matrix: { task: "{{ .steps.plan.out.tasks }}" }
    max_parallel: 2
    seat: dev
    objective: 'sleep 0.3; if [ "{{ .matrix.task }}" = b ] && [ ! -e failed-b ]; then touch failed-b; "$L" done failed error=boom; else "$L" done handoff pr={{ .matrix.task }}; fi'
  - { id: security, needs: [plan], seat: sec, objective: '"$L" done handoff' }
  - id: integrate
    needs: [build, security]
    seat: lead
    objective: '[ "{{ range .steps.build.out }}{{ .pr }}{{ end }}" = abc ] && "$L" done handoff || "$L" done failed'
`))
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	it := e.waitItem(r.ID)
	if it.Kind != inbox.Failed || it.Step != "build" || it.Seat != "dev#2" || it.Title != "step build#2 failed: boom" {
		t.Fatalf("item %+v", it)
	}
	e.waitFor(r.ID, "step.done", 5) // plan, security and the three copies
	time.Sleep(3 * engine.Poll)
	if got := e.stepEvents(r.ID); slices.Index(got, "started:integrate") >= 0 {
		t.Fatalf("integrate started with build#2 held: %v", got)
	}
	e.mustLaight("inbox", it.Short(), "retry")
	r = e.waitEnded(r.ID)
	if r.Status != state.Succeeded {
		t.Fatalf("status %s: %s; steps %v", r.Status, r.Error, e.stepsDone(r.ID))
	}
	evs, _ := state.Events(e.home, r.ID)
	live, most, hops := 0, 0, map[string]int{}
	for _, ev := range evs {
		if ev.Step != "build" {
			continue
		}
		switch ev.Type {
		case "step.started":
			live++
			most = max(most, live)
			hops[ev.Seat]++
		case "step.done":
			live--
		}
	}
	if most != 2 || hops["dev#1"] != 1 || hops["dev#2"] != 2 || hops["dev#3"] != 1 {
		t.Errorf("at most %d copies live (want 2), hops per instance %v", most, hops)
	}
	if n := countEvents(evs, "seat.down", ""); n < 4 { // each instance after its copy, dev#2 twice
		t.Errorf("%d seat.down events", n)
	}
	if countEvents(evs, "seat.up", "dev") != 0 {
		t.Errorf("seat dev started on its own")
	}
	for k := 1; k <= 3; k++ {
		if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "worktrees", spec.Instance("dev", k))); !os.IsNotExist(err) {
			t.Errorf("worktree of dev#%d kept after success: %v", k, err)
		}
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
	if got, err := spec.Render("t", "{{ .steps.verify.out.big }}/{{ .steps.verify.out.nil }}/", r.TemplateData(nil)); err != nil || got != "12345678901234567890//" {
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
