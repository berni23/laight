package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
)

// permission runs the hook in the background and answers its item with answer (or not, when
// answer is ""), returning the item as it was raised and the decision Claude got.
func permission(t *testing.T, home, input string, answer func(it *inbox.Item)) (*inbox.Item, map[string]any) {
	t.Helper()
	pollEvery = 5 * time.Millisecond
	var out, errOut bytes.Buffer
	done := make(chan int)
	go func() { done <- cmdHook([]string{"permission-request"}, strings.NewReader(input), &out, &errOut) }()
	var it *inbox.Item
	for deadline := time.Now().Add(5 * time.Second); it == nil; {
		items, _ := inbox.Pending(home)
		if len(items) > 0 {
			it = items[0]
		} else if time.Now().After(deadline) {
			t.Fatal("no permission item raised")
		}
		time.Sleep(time.Millisecond)
	}
	answer(it)
	select {
	case code := <-done:
		if code != 0 || errOut.Len() > 0 {
			t.Fatalf("hook: %d %s", code, errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hook is still waiting")
	}
	var got struct {
		Out map[string]any `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Out["hookEventName"] != "PermissionRequest" {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	return it, got.Out["decision"].(map[string]any)
}

// A permission request (auto mode escalating, or a seat in default mode) is denied at once:
// no item, nobody asked. The agent is told why and what to do, and the denial is recorded on
// the seat's step, its input redacted.
func TestPermissionHook(t *testing.T) {
	home, id := doneEnv(t)
	t.Setenv("GH_TOKEN", "hunter2-not-a-pattern")
	t.Setenv(inbox.RedactEnv, "GH_TOKEN")
	for _, input := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"curl -H 'x: hunter2-not-a-pattern' https://x","description":"d"}}`,
		`{"tool_name":"Edit","tool_input":{"file_path":"/x/y.go"}}`,
	} {
		var out, errOut bytes.Buffer
		done := make(chan int)
		go func() { done <- cmdHook([]string{"permission-request"}, strings.NewReader(input), &out, &errOut) }()
		select {
		case code := <-done:
			if code != 0 || errOut.Len() > 0 {
				t.Fatalf("hook: %d %s", code, errOut.String())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("the hook waits: a permission must be denied at once")
		}
		var got struct {
			Out struct {
				Decision map[string]any `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.Out.Decision["behavior"] != "deny" || got.Out.Decision["message"] != NoHuman {
			t.Errorf("decision %q: %v", out.String(), err)
		}
	}
	if items, _ := inbox.Pending(home); len(items) > 0 {
		t.Errorf("raised %+v", items[0])
	}
	evs, _ := state.Events(home, id)
	var denied []string
	for _, ev := range evs {
		if ev.Type == "permission.denied" && ev.Step == "verify" && ev.Hop == 3 && ev.Seat == "a" && ev.Data["by"] == "laight" {
			denied = append(denied, fmt.Sprint(ev.Data["tool"], ": ", ev.Data["input"]))
		}
	}
	if want := []string{"Bash: curl -H 'x: ***' https://x", `Edit: {"file_path":"/x/y.go"}`}; !slices.Equal(denied, want) {
		t.Errorf("recorded %q, want %q (events %+v)", denied, want, evs)
	}
}

// Auto mode refusing a call (PermissionDenied) is recorded with its reason; nothing is answered.
// A huge input is cut, after redaction.
func TestPermissionDeniedHook(t *testing.T) {
	home, id := doneEnv(t)
	t.Setenv("GH_TOKEN", "hunter2-not-a-pattern")
	t.Setenv(inbox.RedactEnv, "GH_TOKEN")
	big := strings.Repeat("é", deniedInputMax)
	input := `{"tool_name":"Bash","tool_input":{"command":"echo hunter2-not-a-pattern ` + big + `"},"tool_use_id":"t1","reason":"Irreversible Local Destruction"}`
	var out, errOut bytes.Buffer
	if code := cmdHook([]string{"permission-denied"}, strings.NewReader(input), &out, &errOut); code != 0 || out.Len()+errOut.Len() > 0 {
		t.Fatalf("hook: %d %q %q", code, out.String(), errOut.String())
	}
	evs, _ := state.Events(home, id)
	if len(evs) != 1 || evs[0].Type != "permission.denied" || evs[0].Step != "verify" || evs[0].Data["by"] != "auto mode" ||
		evs[0].Data["reason"] != "Irreversible Local Destruction" {
		t.Fatalf("events: %+v", evs)
	}
	got := evs[0].Data["input"].(string)
	if !strings.HasPrefix(got, "echo *** é") || !strings.HasSuffix(got, "…") || len(got) > deniedInputMax+len("…") || !utf8.ValidString(got) {
		t.Errorf("input %d bytes: %.40q…", len(got), got)
	}
}

// An inbox that can't be written still gets an answer to Claude: deny, never a dialog nobody sees.
func TestPermissionHookNoInbox(t *testing.T) {
	_, _ = doneEnv(t)
	t.Setenv("LAIGHT_RUN", "01NOSUCHRUN000000000000000")
	var out, errOut bytes.Buffer
	cmdHook([]string{"permission-request"}, strings.NewReader(`{"tool_name":"Bash"}`), &out, &errOut)
	if !strings.Contains(out.String(), `"behavior":"deny"`) {
		t.Errorf("no run: %q %q", out.String(), errOut.String())
	}
}

// A question answered in Claude's own dialog: PostToolUse for another call leaves its item
// alone; for the same call it withdraws it, and the waiting hook returns.
func TestPermissionAnsweredInPane(t *testing.T) {
	home, _ := doneEnv(t)
	_, d := permission(t, home, ask, func(it *inbox.Item) {
		post := func(input string) {
			var out, errOut bytes.Buffer
			if code := cmdHook([]string{"post-tool-use"}, strings.NewReader(input), &out, &errOut); code != 0 || out.Len()+errOut.Len() > 0 {
				t.Fatalf("post-tool-use: %d %q %q", code, out.String(), errOut.String())
			}
		}
		post(`{"tool_name":"Bash","tool_input":{"command":"pwd","description":"list"}}`)
		post(strings.Replace(ask, "which fix?", "which other fix?", 1))
		if _, err := inbox.Get(home, it.ID); err != nil {
			t.Errorf("another call withdrew the item: %v", err)
		}
		post(ask)
	})
	if d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "withdrawn") {
		t.Errorf("withdrawn: %v", d)
	}
}

// A request the hook can't read (too big, not JSON, no tool) is denied at once: an item would
// show nothing, and an allow would allow what nobody saw. A PostToolUse it can't read withdraws
// nothing.
func TestPermissionHookUnreadable(t *testing.T) {
	home, _ := doneEnv(t)
	defer func(n int64) { hookInputMax = n }(hookInputMax)
	hookInputMax = 200
	for _, input := range []string{
		`{"tool_name":"Write","tool_input":{"content":"` + strings.Repeat("x", 300) + `"}}`,
		`not json`,
		`{"tool_input":{"command":"ls"}}`,
	} {
		var out, errOut bytes.Buffer
		cmdHook([]string{"permission-request"}, strings.NewReader(input), &out, &errOut)
		if !strings.Contains(out.String(), `"behavior":"deny"`) {
			t.Errorf("%.20s…: %q %q", input, out.String(), errOut.String())
		}
		if items, _ := inbox.Pending(home); len(items) > 0 {
			t.Errorf("%.20s…: raised %+v", input, items[0])
			inbox.Drop(home, items[0].ID)
		}
	}

	it := inbox.Item{ID: "01ZERO", Run: os.Getenv("LAIGHT_RUN"), Seat: "a", Kind: inbox.Question, Title: "t",
		Questions: []inbox.Ask{{Question: "q"}}, Match: toolMatch(hookInput{})}
	if err := inbox.Put(home, it); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmdHook([]string{"post-tool-use"}, strings.NewReader(`{"tool_name":"Write","tool_input":{"content":"`+strings.Repeat("x", 300)+`"}}`), &out, &errOut)
	if _, err := inbox.Get(home, it.ID); err != nil {
		t.Errorf("an unreadable PostToolUse withdrew an item: %v", err)
	}
}

// Claude stops a hook it no longer waits for (Esc in the pane): the hook drops its item,
// records no answer, and denies.
func TestPermissionHookCancelled(t *testing.T) {
	home, _ := doneEnv(t)
	it, d := permission(t, home, ask, func(*inbox.Item) {
		syscall.Kill(os.Getpid(), syscall.SIGHUP)
	})
	if d["behavior"] != "deny" || d["message"] != "cancelled" {
		t.Errorf("cancelled: %v", d)
	}
	if _, err := inbox.Get(home, it.ID); err == nil {
		t.Error("the item is still pending")
	}
	if done, _ := inbox.Answered(home, it.ID); done != nil {
		t.Errorf("an answer was recorded: %+v", done)
	}
}

// A run that ended before the item was raised has withdrawn its items already: the hook drops
// its own and denies, instead of leaving it to expire.
func TestPermissionHookRunEnded(t *testing.T) {
	home, id := doneEnv(t)
	if _, err := state.Update(home, id, func(r *state.Run) error { r.Status = state.Aborted; return nil }); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmdHook([]string{"permission-request"}, strings.NewReader(ask), &out, &errOut)
	if !strings.Contains(out.String(), `"behavior":"deny"`) || !strings.Contains(out.String(), "run ended") {
		t.Errorf("hook: %q %q", out.String(), errOut.String())
	}
	if items, _ := inbox.Pending(home); len(items) > 0 {
		t.Errorf("left behind: %+v", items[0])
	}
}

// The same call asked twice is answered in order, one PostToolUse per item, and only the
// seat's own: another seat's identical call is another prompt. A withdrawal is an answer in
// the event log.
func TestPermissionAnsweredInPaneOrder(t *testing.T) {
	home, id := doneEnv(t)
	call := hookInput{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`)}
	put := func(ref, seat string) {
		it := inbox.Item{ID: "01" + ref, Run: id, Seat: seat, Kind: inbox.Question, Title: "t",
			Questions: []inbox.Ask{{Question: "q"}}, Match: toolMatch(call)}
		if err := inbox.Put(home, it); err != nil {
			t.Fatal(err)
		}
	}
	put("A0", "b") // the oldest, but another seat's
	put("A1", "a")
	put("C1", "a")
	var out, errOut bytes.Buffer
	cmdHook([]string{"post-tool-use"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &out, &errOut)
	var left []string
	items, _ := inbox.Pending(home)
	for _, it := range items {
		left = append(left, it.ID)
	}
	if strings.Join(left, " ") != "01A0 01C1" {
		t.Errorf("pending after one PostToolUse: %v", left)
	}
	evs, _ := state.Events(home, id)
	var answered []string
	for _, ev := range evs {
		if ev.Type == "inbox.answered" {
			answered = append(answered, fmt.Sprint(ev.Data["item"], "=", ev.Data["answer"]))
		}
	}
	if strings.Join(answered, " ") != "01A1=in the pane" {
		t.Errorf("answered events: %v", answered)
	}
}

// PostToolUse runs after every tool call: one that withdraws nothing writes nothing, and the
// permission hook's own events say what it did.
func TestToolHooksLogOnlyWhatTheyDo(t *testing.T) {
	home, id := doneEnv(t)
	var out, errOut bytes.Buffer
	cmdHook([]string{"post-tool-use"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &out, &errOut)
	cmdHook([]string{"permission-request"}, strings.NewReader(`not json`), &out, &errOut)
	if evs, _ := state.Events(home, id); len(evs) > 0 {
		t.Errorf("events: %+v", evs)
	}
}
