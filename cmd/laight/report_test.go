package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// A red run's report names the step, hop, seat, kind and reason, quotes the end of the pane,
// lists the requests its seats were denied, and says the worktrees were kept. A green run's
// report still lists its denials.
func TestReport(t *testing.T) {
	home := t.TempDir()
	id := state.NewID(time.Now())
	r := &state.Run{ID: id, Workflow: "wf", Status: state.Failed, Error: "step review failed: no PR",
		Spec:  &spec.Workflow{OnFinish: spec.OnFinish{Failure: "keep"}},
		Seats: map[string]*state.Seat{"rev": {Dir: t.TempDir(), Worktree: true}, "impl": {Dir: home}},
		Failure: &state.Failure{Step: "review", Hop: 3, Seat: "rev", Kind: state.KindExit, Reason: "step review failed: no PR",
			Detail: "\n$ gh pr view\nno pull requests found\n", At: time.Now()}}
	if err := state.Create(home, r); err != nil {
		t.Fatal(err)
	}
	if err := state.Append(home, state.Event{Run: id, Type: "permission.denied", Step: "implement", Seat: "impl",
		Data: map[string]any{"tool": "Bash", "input": "git push --force\nmore", "by": "laight"}}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	report(&out, home, r)
	want := "run " + r.Short() + " failed\n" +
		"  ✕ review (hop 3, seat rev): exit\n" +
		"    step review failed: no PR\n" +
		"    │ $ gh pr view\n" +
		"    │ no pull requests found\n" +
		"  ⊘ 1 request(s) denied:\n" +
		"    implement (impl) Bash: git push --force (nobody to ask)\n" +
		"  1 worktree(s) kept (on_finish.failure: keep); laight abort " + r.Short() + " deletes them\n"
	if out.String() != want {
		t.Errorf("report:\n%s\nwant:\n%s", out.String(), want)
	}

	r.Seats["rev"].Dir = filepath.Join(home, "gone") // removed: nothing to say about it
	out.Reset()
	report(&out, home, r)
	if strings.Contains(out.String(), "kept") {
		t.Errorf("a removed worktree reported as kept:\n%s", out.String())
	}

	r.Status, r.Failure, r.Error = state.Succeeded, nil, ""
	out.Reset()
	report(&out, home, r)
	if got := out.String(); !strings.HasPrefix(got, "run "+r.Short()+" succeeded\n  ⊘ 1 request(s) denied:") || strings.Contains(got, "worktrees kept") {
		t.Errorf("green report:\n%s", got)
	}
}
