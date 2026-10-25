package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// ask is an AskUserQuestion call: a single-select question whose second option carries a secret
// (shown redacted, answered with the agent's own label), and a multi-select one.
const ask = `{"tool_name":"AskUserQuestion","tool_input":{"questions":[
	{"question":"#115: which fix?","header":"Fix","multiSelect":false,"options":[
		{"label":"Reject at boundary","description":"ValidateStructure refuses it"},
		{"label":"Use hunter2-not-a-pattern","description":"d"}]},
	{"question":"Which tests?","header":"Tests","multiSelect":true,"options":[
		{"label":"unit"},{"label":"integration"},{"label":"e2e"}]}]}}`

// inboxCmd runs laight inbox and returns its exit code and output.
func inboxCmd(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := cmdInbox(args, &out, &errs)
	return code, out.String() + errs.String()
}

// liveEngine gives the run an engine window on laight's tmux server for the test's home, so
// `laight inbox` takes answers to its items.
func liveEngine(t *testing.T, home, id string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	srv := tmux.Server{Socket: state.Socket(home)}
	t.Cleanup(func() { srv.KillServer() })
	r, err := state.Update(home, id, func(r *state.Run) error { r.Session = state.SessionName("wf", id); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.NewSession(r.Session, tmux.Window{Name: rig.EngineWindow, Cmd: "sleep 600"}); err != nil {
		t.Fatal(err)
	}
}

// A question the agent asks is a question item: the question and its options, listed and shown
// in full, answered by number (several for a multi-select) or in words, and given back to Claude
// as the user's answers, keyed and labelled as the agent wrote them.
func TestQuestionHook(t *testing.T) {
	home, id := doneEnv(t)
	liveEngine(t, home, id)
	t.Setenv("GH_TOKEN", "hunter2-not-a-pattern")
	t.Setenv(inbox.RedactEnv, "GH_TOKEN")

	it, d := permission(t, home, ask, func(it *inbox.Item) {
		if it.Kind != inbox.Question || it.Title != "a asks 2 questions: #115: which fix? …" || len(it.Choices) != 0 ||
			len(it.Questions) != 2 || it.Questions[0].Options[1].Label != "Use ***" || !it.Questions[1].MultiSelect || it.Step != "verify" {
			t.Errorf("item: %+v", it)
		}
		code, out := inboxCmd(t)
		for _, want := range []string{"a asks 2 questions", "Q1. #115: which fix?", "1. Reject at boundary", "2. Use ***", "3. e2e", "1,3", "answer with"} {
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("list has no %q: %d\n%s", want, code, out)
			}
		}
		if strings.Contains(out, "hunter2") || strings.Contains(out, "ValidateStructure") {
			t.Errorf("list shows a secret or a description:\n%s", out)
		}
		code, out = inboxCmd(t, it.Short())
		for _, want := range []string{"ValidateStructure refuses it", "look: laight attach", "laight inbox " + it.Short() + " <answer> <answer>"} {
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("show has no %q: %d\n%s", want, code, out)
			}
		}
		for _, bad := range [][]string{{it.Short(), "2"}, {it.Short(), "1,2", "1"}, {it.Short(), "2", ""}, {it.Short(), "allow"}} {
			if code, out := inboxCmd(t, bad...); code == 0 {
				t.Errorf("%q answered it: %s", bad, out)
			}
		}
		if code, out := inboxCmd(t, it.Short(), "2", "1,3"); code != 0 || !strings.Contains(out, "Use ***; unit, e2e") {
			t.Errorf("answer: %d %s", code, out)
		}
	})
	if d["behavior"] != "allow" {
		t.Fatalf("decision: %v", d)
	}
	in := d["updatedInput"].(map[string]any)
	answers, _ := in["answers"].(map[string]any)
	if answers["#115: which fix?"] != "Use hunter2-not-a-pattern" || answers["Which tests?"] != "unit, e2e" || len(in["questions"].([]any)) != 2 {
		t.Errorf("updated input: %v", in)
	}
	if done, _ := inbox.Answered(home, it.ID); done == nil || strings.Join(done.Answers, "|") != "Use ***|unit, e2e" {
		t.Errorf("recorded: %+v", done)
	}

	// In words, for a single question.
	one := `{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Colour?","options":[{"label":"Red"},{"label":"Green"}]}]}}`
	_, d = permission(t, home, one, func(it *inbox.Item) {
		if it.Title != "a asks: Colour?" {
			t.Errorf("title %q", it.Title)
		}
		if code, out := inboxCmd(t, it.Short(), "neither: purple"); code != 0 {
			t.Errorf("answer: %s", out)
		}
	})
	if a := d["updatedInput"].(map[string]any)["answers"].(map[string]any); a["Colour?"] != "neither: purple" {
		t.Errorf("answers %v", a)
	}

	// A call laight can't read as questions still asks: for permission, showing its input.
	odd := `{"tool_name":"AskUserQuestion","tool_input":{"questions":"what?"}}`
	it, _ = permission(t, home, odd, func(it *inbox.Item) { inbox.Answer(home, it.ID, inbox.Deny) })
	if it.Kind != inbox.Permission || !strings.Contains(it.Detail, "what?") {
		t.Errorf("unreadable question: %+v", it)
	}
}

// Answered in the pane: PostToolUse's input has gained the answers, and still withdraws the item.
func TestQuestionAnsweredInPane(t *testing.T) {
	home, _ := doneEnv(t)
	_, d := permission(t, home, ask, func(it *inbox.Item) {
		post := strings.Replace(ask, `"questions":[`, `"answers":{"#115: which fix?":"Reject at boundary"},"annotations":{},"questions":[`, 1)
		var out, errOut bytes.Buffer
		cmdHook([]string{"post-tool-use"}, strings.NewReader(post), &out, &errOut)
	})
	if d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "withdrawn") {
		t.Errorf("withdrawn: %v", d)
	}
}

// Nobody answers a question: the hook denies, and records that nobody answered.
func TestQuestionExpires(t *testing.T) {
	home, _ := doneEnv(t)
	defer func(w time.Duration) { permissionWait = w }(permissionWait)
	permissionWait = 50 * time.Millisecond
	it, d := permission(t, home, ask, func(*inbox.Item) {})
	if d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "nobody answered") || d["updatedInput"] != nil {
		t.Errorf("expired: %v", d)
	}
	if done, _ := inbox.Answered(home, it.ID); done == nil || len(done.Answers) != 2 {
		t.Errorf("recorded: %+v", done)
	}
	evs, _ := state.Events(home, it.Run)
	if n := len(evs); n == 0 || evs[n-1].Data["expired"] != true {
		t.Errorf("events: %+v", evs)
	}
}
