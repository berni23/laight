package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berni23/laight/internal/inbox"
)

// transcript writes a Claude transcript of the given entries and returns its path.
func transcript(t *testing.T, entries ...string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(f, []byte(strings.Join(entries, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func prompt(text string) string {
	return `{"type":"user","message":{"role":"user","content":` + quoted(text) + `}}`
}

func said(blocks ...string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[` + strings.Join(blocks, ",") + `]}}`
}

func text(s string) string { return `{"type":"text","text":` + quoted(s) + `}` }

func tool(name, input string) string {
	return `{"type":"tool_use","id":"t","name":"` + name + `","input":` + input + `}`
}

const result = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"secret output"}]}}`

func quoted(s string) string { b, _ := json.Marshal(s); return string(b) }

// What an agent said before asking is its current turn's text and tool calls: not the prompt,
// not an earlier turn, not thinking or tool results, and not the question itself.
func TestTurnContext(t *testing.T) {
	f := transcript(t,
		`{"type":"permission-mode","permissionMode":"auto"}`,
		prompt("an earlier step"),
		said(text("earlier turn")),
		prompt("Implement issue 7"),
		`{"type":"attachment","attachment":{}}`,
		said(`{"type":"thinking","thinking":"hmm"}`),
		said(text("Two config loaders exist; the issue says keep one.")),
		said(tool("Bash", `{"command":"grep -rn toml internal/\necho more"}`)),
		result,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"a reminder"}}`,
		said(tool("Read", `{"file_path":"/w/internal/config/toml.go"}`), tool("TodoWrite", `{"todos":[]}`)),
		result,
		said(text("Which one stays is your call."), tool("AskUserQuestion", `{"questions":[{"question":"Which?"}]}`)),
	)
	want := "Two config loaders exist; the issue says keep one.\n" +
		"  ran: Bash  grep -rn toml internal/ …\n" +
		"  ran: Read  /w/internal/config/toml.go\n" +
		"  ran: TodoWrite\n" +
		"Which one stays is your call."
	if got := turnContext(f, ""); got != want {
		t.Errorf("context:\n%s\nwant:\n%s", got, want)
	}

	// A prompt given as text blocks starts a turn too; a long turn keeps its end.
	long := strings.Repeat("line of work\n", 300)
	f = transcript(t, said(text("before")), `{"type":"user","message":{"content":[{"type":"text","text":"go"}]}}`,
		said(text(long+"the end")))
	got := turnContext(f, "")
	if len(got) > contextMax+4 || !strings.HasPrefix(got, "… line of work") || !strings.HasSuffix(got, "the end") || strings.Contains(got, "before") {
		t.Errorf("long context (%d bytes): %q…", len(got), got[:40])
	}

	if got := turnContext(filepath.Join(t.TempDir(), "missing.jsonl"), ""); got != "" {
		t.Errorf("no transcript: %q", got)
	}
}

// Claude writes the question's own message only once it is answered: what the agent said just
// before asking comes from its screen (as Claude 2.1.314 draws it).
func TestLastSaid(t *testing.T) {
	rule := strings.Repeat("─", 60)
	screen := strings.Join([]string{
		"❯ This is a test.", "", "⏺ I'm about to list the repo's top folder.", "", "  Listed 1 directory", "",
		"⏺ The top folder is a Go project.", "", "  Two loaders exist.  ", rule, " ☐ Folder ", "", "Which folder?",
		"❯ 1. cmd", rule, "  4. Chat about this",
	}, "\n")
	if got := lastSaid(screen); got != "The top folder is a Go project.\n\nTwo loaders exist." {
		t.Errorf("said: %q", got)
	}
	for _, screen := range []string{"⏺ Bash(ls)\n" + rule, "no agent here", ""} {
		if got := lastSaid(screen); got != "" {
			t.Errorf("%q said %q", screen, got)
		}
	}

	f := transcript(t, prompt("go"), said(text("first")))
	if got := turnContext(f, "just before asking"); got != "first\njust before asking" {
		t.Errorf("context: %q", got)
	}
	f = transcript(t, prompt("go"), said(text("already  written")), said(tool("AskUserQuestion", `{}`)))
	if got := turnContext(f, "already written"); got != "already  written" {
		t.Errorf("said twice: %q", got)
	}
}

// A question item carries what the agent said before asking, redacted, and the inbox shows it.
func TestQuestionContext(t *testing.T) {
	home, id := doneEnv(t)
	liveEngine(t, home, id)
	f := transcript(t, prompt("step"), said(text("Found two loaders, token ghp_abcdefghijklmnopqrstuvwxyz0123456789 aside.")),
		said(tool("AskUserQuestion", `{}`)))
	in := `{"tool_name":"AskUserQuestion","transcript_path":` + quoted(f) +
		`,"tool_input":{"questions":[{"question":"Keep which?","options":[{"label":"yaml"},{"label":"toml"}]}]}}`
	permission(t, home, in, func(it *inbox.Item) {
		if !strings.HasPrefix(it.Detail, "Found two loaders, token ***") {
			t.Errorf("detail: %q", it.Detail)
		}
		code, out := inboxCmd(t, it.Short())
		if code != 0 || !strings.Contains(out, "what a said before asking:\n    Found two loaders") {
			t.Errorf("show: %d\n%s", code, out)
		}
		inboxCmd(t, it.Short(), "1")
	})
}
