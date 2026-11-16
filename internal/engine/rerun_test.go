package engine

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// redRun is a run of the review/fix cycle that failed at fix (hop 3), with impl's worktree kept
// and a conversation on each seat.
func redRun(t *testing.T) (*state.Run, *spec.Workflow) {
	t.Helper()
	w := &spec.Workflow{ID: "wf", LoopGuards: spec.LoopGuards{MaxHops: 10}, Seats: map[string]*spec.Seat{
		"impl": {Runtime: "claude-code", Cwd: "worktree", Session: "resume"},
		"rev":  {Runtime: "claude-code", Cwd: "repo", Session: "fresh"},
	}, Steps: []*spec.Step{
		agent("implement", "impl", map[string]string{"handoff": "review"}),
		agent("review", "rev", map[string]string{"handoff": "fix"}),
		agent("fix", "impl", nil),
	}}
	wt := t.TempDir()
	old := &state.Run{ID: state.NewID(time.Now()), Workflow: "wf", Repo: "/repo", Spec: w, SpecDir: "/repo/.laight",
		Base: "origin/main", Status: state.Failed, Hops: 3, Inputs: map[string]string{"issue": "7"},
		Seats: map[string]*state.Seat{
			"impl": {Window: "impl", Dir: wt, Worktree: true, SessionID: "s-impl", SetupDone: true, Up: true, Step: "fix", Hop: 3, Given: 2},
			"rev":  {Window: "rev", Dir: "/repo", SessionID: "s-rev", Given: 1},
		},
		Steps: map[string]*state.StepResult{
			"implement": {Hop: 1, Exit: "handoff", Out: map[string]any{"pr": "42"}},
			"review":    {Hop: 2, Exit: "handoff"},
			"fix":       {Hop: 3, Exit: spec.ExitFailed, Out: map[string]any{"reason": "denied"}},
		},
		Failure: &state.Failure{Step: "fix", Hop: 3, Seat: "impl", Kind: state.KindPermission, Reason: "step fix failed: denied"},
	}
	return old, w
}

// A rerun starts again at the failed step, keeps what finished before it, the hop count, the
// worktree where it is and the conversation of a seat that resumes; a fresh seat's isn't kept.
func TestRerunFromFailedStep(t *testing.T) {
	old, w := redRun(t)
	r, err := Rerun(old, w, old.Inputs, "NEW", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Queue, []string{"fix"}) || r.Hops != 3 || r.RerunOf != old.ID || r.Base != old.Base {
		t.Errorf("queue %v, hops %d, rerun of %q, base %q", r.Queue, r.Hops, r.RerunOf, r.Base)
	}
	if len(r.Steps) != 2 || r.Steps["implement"].Out["pr"] != "42" || r.Steps["fix"] != nil {
		t.Errorf("steps %v", r.Steps)
	}
	impl, rev := r.Seats["impl"], r.Seats["rev"]
	if impl == nil || impl.Dir != old.Seats["impl"].Dir || !impl.Worktree || !impl.SetupDone || impl.SessionID != "s-impl" || !impl.Rerun {
		t.Errorf("impl %+v", impl)
	}
	if impl.Up || impl.Step != "" || impl.Hop != 0 || impl.Given != 0 || impl.Turn {
		t.Errorf("impl carried the old run's live state: %+v", impl)
	}
	if rev == nil || rev.SessionID != "" || rev.Rerun || rev.Worktree {
		t.Errorf("rev (session: fresh) %+v", rev)
	}
	if r.Status != state.Starting || r.Session != state.SessionName("wf", "NEW") {
		t.Errorf("status %s, session %s", r.Status, r.Session)
	}
}

// Steps routed to but not started, and hops an abort cut short, start again; an aborted run can
// be rerun too.
func TestRerunQueuedAndCutShort(t *testing.T) {
	old, w := redRun(t)
	old.Status, old.Failure = state.Aborted, &state.Failure{Kind: state.KindAborted, Step: "review", Hop: 2}
	delete(old.Steps, "fix")
	delete(old.Steps, "review")
	old.Active = []*state.Hop{{Step: "review", Seat: "rev", N: 2}}
	old.Queue = []string{"fix"}
	r, err := Rerun(old, w, old.Inputs, "NEW", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Queue, []string{"fix", "review"}) || r.Active != nil {
		t.Errorf("queue %v, active %v", r.Queue, r.Active)
	}
}

// A matrix step goes on with its expansion: only the copies that failed, were cut short or never
// started run again.
func TestRerunMatrixCopies(t *testing.T) {
	old, w := redRun(t)
	w.Steps = append(w.Steps, matrixStep("build", "dev", 2, []any{"a", "b", "c", "d"}))
	w.Seats["dev"] = &spec.Seat{Runtime: "claude-code", Cwd: "worktree"}
	old.Steps = map[string]*state.StepResult{"implement": old.Steps["implement"]}
	old.Failure = &state.Failure{Step: "build", Copy: 2, Hop: 5, Kind: state.KindExit}
	old.Matrix = map[string]*state.MatrixRun{"build": {
		Copies:  []map[string]any{{"task": "a"}, {"task": "b"}, {"task": "c"}, {"task": "d"}},
		Pending: []int{4},
		Results: []*state.StepResult{{Hop: 4, Exit: "handoff"}, {Hop: 5, Exit: spec.ExitFailed}, nil, nil},
	}}
	old.Active = []*state.Hop{{Step: "build", Seat: "dev#3", N: 6, Copy: 3}}
	r, err := Rerun(old, w, old.Inputs, "NEW", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m := r.Matrix["build"]
	if m == nil || !slices.Equal(m.Pending, []int{2, 3, 4}) || m.Results[0] == nil || m.Results[1] != nil || len(r.Queue) != 0 {
		t.Fatalf("matrix %+v, queue %v", m, r.Queue)
	}
	if old.Matrix["build"].Results[1] == nil {
		t.Error("the old run's results were changed")
	}
}

// A run that failed before its first hop starts over, in the worktrees it made (it hands them
// over: left behind, no run would list them); one that left nothing unfinished can't be rerun.
func TestRerunStartOverOrNothing(t *testing.T) {
	old, w := redRun(t)
	old.Hops, old.Steps, old.Failure = 0, nil, &state.Failure{Kind: state.KindCredential, Reason: "no secret"}
	old.Seats["impl"].Given, old.Seats["rev"].Given = 0, 0 // spin-up gave them sessions, no steps
	r, err := Rerun(old, w, old.Inputs, "NEW", time.Now())
	if err != nil || len(r.Queue) != 0 || r.Hops != 0 {
		t.Fatalf("start over: %+v, %v", r, err)
	}
	if s := r.Seats["impl"]; s == nil || s.Dir != old.Seats["impl"].Dir || !s.Worktree || s.SessionID != "" || s.Rerun {
		t.Errorf("impl on start-over: %+v", s)
	}

	old, w = redRun(t)
	old.Failure = &state.Failure{Kind: state.KindCrash, Reason: "engine"}
	delete(old.Steps, "fix")
	if _, err := Rerun(old, w, old.Inputs, "NEW", time.Now()); err == nil || !strings.Contains(err.Error(), "nothing to rerun") {
		t.Errorf("err %v", err)
	}
}

// Shape only (#66): a step that finished or starts again must still be there, and a matrix step
// must still be one; anything else may change.
func TestRerunShape(t *testing.T) {
	cases := map[string]struct {
		change func(*spec.Workflow)
		want   string
	}{
		"objective changed": {func(w *spec.Workflow) { w.Step("fix").Objective = "do better" }, ""},
		"step added":        {func(w *spec.Workflow) { w.Steps = append(w.Steps, agent("merge", "impl", nil)) }, ""},
		"passed step gone":  {func(w *spec.Workflow) { w.Steps = slices.Delete(w.Steps, 0, 1) }, "step implement (it finished in run"},
		"failed step gone":  {func(w *spec.Workflow) { w.Steps = w.Steps[:2] }, "step fix is no longer in the workflow"},
		"became a matrix": {func(w *spec.Workflow) { w.Step("review").Matrix = map[string]any{"x": []any{1}} },
			"step review changed between a matrix step and a plain one"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			old, w := redRun(t)
			now := *w
			now.Steps = make([]*spec.Step, len(w.Steps))
			for i, st := range w.Steps {
				cp := *st
				now.Steps[i] = &cp
			}
			c.change(&now)
			_, err := Rerun(old, &now, old.Inputs, "NEW", time.Now())
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("err %v; want %q", err, c.want)
			}
		})
	}
}

// A seat whose cwd or runtime changed, or whose worktree is gone, starts fresh; a seat the
// workflow dropped keeps only its worktree, so the rerun still cleans it up.
func TestRerunSeats(t *testing.T) {
	old, w := redRun(t)
	now := *w
	now.Seats = map[string]*spec.Seat{"impl": {Runtime: "claude-code", Cwd: "repo", Session: "resume"}, "rev": w.Seats["rev"]}
	r, err := Rerun(old, &now, old.Inputs, "NEW", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Seats["impl"]; s.Worktree || s.Dir != "" || s.SessionID != "" {
		t.Errorf("impl moved to the repo kept %+v", s)
	}
	if !slices.Equal(r.Kept, []string{old.Seats["impl"].Dir}) { // the rerun cleans it up
		t.Errorf("kept %v; want impl's old worktree", r.Kept)
	}

	old, w = redRun(t)
	old.Seats["impl"].Dir = filepath.Join(t.TempDir(), "gone")
	r, _ = Rerun(old, w, old.Inputs, "NEW", time.Now())
	if s := r.Seats["impl"]; s.Worktree || s.SessionID != "" || s.SetupDone {
		t.Errorf("impl with its worktree gone kept %+v", s)
	}

	old, w = redRun(t)
	old.Seats["old"] = &state.Seat{Window: "old", Dir: t.TempDir(), Worktree: true, SessionID: "x"}
	r, _ = Rerun(old, w, old.Inputs, "NEW", time.Now())
	if s := r.Seats["old"]; s != nil || !slices.Contains(r.Kept, old.Seats["old"].Dir) {
		t.Errorf("dropped seat: %+v, kept %v", s, r.Kept)
	}

	// A seat never given a step had no conversation: it gets no note.
	old, w = redRun(t)
	old.Seats["rev"].Given = 0
	w.Seats["rev"].Session = "resume"
	r, _ = Rerun(old, w, old.Inputs, "NEW", time.Now())
	if s := r.Seats["rev"]; s.SessionID != "" || s.Rerun {
		t.Errorf("rev, never given a step: %+v", s)
	}
}

// Only a finished red run can be rerun, and only once.
func TestRerunRefused(t *testing.T) {
	for name, set := range map[string]func(*state.Run){
		"live":     func(r *state.Run) { r.Status = state.Running },
		"green":    func(r *state.Run) { r.Status = state.Succeeded },
		"rerun":    func(r *state.Run) { r.RerunAs = "X" },
		"other wf": func(r *state.Run) { r.Workflow = "other" },
	} {
		old, w := redRun(t)
		set(old)
		if _, err := Rerun(old, w, old.Inputs, "NEW", time.Now()); err == nil {
			t.Errorf("%s: rerun allowed", name)
		}
	}
}

// The note pasted before a resumed seat's first step says which run failed and why.
func TestRerunNote(t *testing.T) {
	n := RerunNote("01ABCDEFGHJKMN", &state.Failure{Kind: state.KindPermission, Reason: "step fix failed: denied"})
	if !strings.Contains(n, "rerun of run ghjkmn") || !strings.Contains(n, "(permission): step fix failed: denied") {
		t.Errorf("note %q", n)
	}
}
