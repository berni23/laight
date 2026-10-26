package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
	if code, out := inboxCmd(t, it.Short(), "1", "1"); code == 0 || !strings.Contains(out, "already answered") {
		t.Errorf("a late answer: %d %s", code, out)
	}

	// In words, for a single question.
	one := `{"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Colour?","options":[{"label":"Red"},{"label":"Green"}]}]}}`
	_, d = permission(t, home, one, func(it *inbox.Item) {
		if it.Title != "a asks: Colour?" {
			t.Errorf("title %q", it.Title)
		}
		if code, out := inboxCmd(t, it.Short(), "neither: purple, ghp_cccccccccccccccccccccccccccccc"); code != 0 {
			t.Errorf("answer: %s", out)
		}
	})
	// The agent gets the words as typed; the run's events, which aren't for agents, don't.
	if a := d["updatedInput"].(map[string]any)["answers"].(map[string]any); a["Colour?"] != "neither: purple, ghp_cccccccccccccccccccccccccccccc" {
		t.Errorf("answers %v", a)
	}
	if b, err := os.ReadFile(filepath.Join(state.Dir(home, id), "events.jsonl")); err != nil || bytes.Contains(b, []byte("ghp_ccc")) || !bytes.Contains(b, []byte("neither: purple")) {
		t.Errorf("events keep a typed secret: %v\n%s", err, b)
	}

	// Options that look the same once redacted: a number picks its own option, and words that
	// read like a shown label go as typed.
	twins := `{"tool_name":"AskUserQuestion","tool_input":{"questions":[
		{"question":"Token?","options":[{"label":"Use ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"label":"Use ghp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]},
		{"question":"Again?","options":[{"label":"Use ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"label":"Use ghp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}]}}`
	_, d = permission(t, home, twins, func(it *inbox.Item) {
		if it.Questions[0].Options[0].Label != it.Questions[0].Options[1].Label {
			t.Errorf("options not alike once redacted: %+v", it.Questions[0])
		}
		if code, out := inboxCmd(t, it.Short(), "2", "Use ***"); code != 0 {
			t.Errorf("answer: %s", out)
		}
	})
	if a := d["updatedInput"].(map[string]any)["answers"].(map[string]any); a["Token?"] != "Use ghp_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || a["Again?"] != "Use ***" {
		t.Errorf("answers %v", a)
	}

	// A call laight can't read as questions is denied at once: an allow would leave Claude's own
	// dialog waiting in a pane, with nothing in the inbox to answer.
	odd := `{"tool_name":"AskUserQuestion","tool_input":{"questions":"what?"}}`
	var out, errs bytes.Buffer
	if code := cmdHook([]string{"permission-request"}, strings.NewReader(odd), &out, &errs); code != 0 ||
		!strings.Contains(out.String(), `"behavior":"deny"`) || !strings.Contains(out.String(), "AskUserQuestion") {
		t.Errorf("unreadable question: %d %s %s", code, out.String(), errs.String())
	}
	if items, _ := inbox.Pending(home); len(items) > 0 {
		t.Errorf("unreadable question raised %+v", items[0])
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
