package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

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

func TestPermissionHook(t *testing.T) {
	home, _ := doneEnv(t)
	t.Setenv("GH_TOKEN", "hunter2-not-a-pattern")
	t.Setenv(inbox.RedactEnv, "GH_TOKEN")
	bash := `{"tool_name":"Bash","tool_input":{"command":"curl -H 'x: hunter2-not-a-pattern' https://x","description":"d"}}`

	it, d := permission(t, home, bash, func(it *inbox.Item) {
		if _, err := inbox.Answer(home, it.ID, inbox.Allow); err != nil {
			t.Fatal(err)
		}
	})
	if d["behavior"] != "allow" {
		t.Errorf("allowed: %v", d)
	}
	if it.Kind != inbox.Permission || it.Step != "verify" || it.Hop != 3 || it.Seat != "a" || it.Expires == nil ||
		it.Title != "a wants to use Bash" || it.Detail != "curl -H 'x: ***' https://x" {
		t.Errorf("item: %+v", it)
	}

	_, d = permission(t, home, `{"tool_name":"Edit","tool_input":{"file_path":"/x/y.go"}}`, func(it *inbox.Item) {
		if it.Detail != `{"file_path":"/x/y.go"}` {
			t.Errorf("edit detail %q", it.Detail)
		}
		inbox.Answer(home, it.ID, inbox.Deny)
	})
	if d["behavior"] != "deny" || d["message"] == "" {
		t.Errorf("denied: %v", d)
	}

	// The run ended: its items are withdrawn, and the hook denies.
	_, d = permission(t, home, bash, func(it *inbox.Item) { inbox.Withdraw(home, it.Run) })
	if d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "run ended") {
		t.Errorf("withdrawn: %v", d)
	}
}

// Nobody answers: the hook denies before Claude's own timeout, and records the deny as the
// item's answer, so a late answer hears it was late.
func TestPermissionHookExpires(t *testing.T) {
	home, _ := doneEnv(t)
	defer func(w time.Duration) { permissionWait = w }(permissionWait)
	permissionWait = 50 * time.Millisecond
	it, d := permission(t, home, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, func(*inbox.Item) {})
	if d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "nobody answered") {
		t.Errorf("expired: %v", d)
	}
	if done, _ := inbox.Answered(home, it.ID); done == nil || done.Answer != inbox.Deny {
		t.Errorf("answer recorded: %+v", done)
	}
	evs, _ := state.Events(home, it.Run)
	if n := len(evs); n == 0 || evs[n-1].Type != "inbox.answered" || evs[n-1].Data["item"] != it.ID || evs[n-1].Data["answer"] != inbox.Deny || evs[n-1].Data["expired"] != true {
		t.Errorf("events: %+v", evs) // the last one says the item expired
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

// A prompt allowed in Claude's own dialog: PostToolUse for the same call withdraws the item, and
// the waiting hook returns. Another call's PostToolUse leaves it alone.
func TestPermissionAnsweredInPane(t *testing.T) {
	home, _ := doneEnv(t)
	_, d := permission(t, home, `{"tool_name":"Bash","tool_input":{"command":"ls","description":"list"}}`, func(it *inbox.Item) {
		post := func(input string) {
			var out, errOut bytes.Buffer
			if code := cmdHook([]string{"post-tool-use"}, strings.NewReader(input), &out, &errOut); code != 0 || out.Len()+errOut.Len() > 0 {
				t.Fatalf("post-tool-use: %d %q %q", code, out.String(), errOut.String())
			}
		}
		post(`{"tool_name":"Bash","tool_input":{"command":"pwd","description":"list"}}`)
		if _, err := inbox.Get(home, it.ID); err != nil {
			t.Errorf("another call withdrew the item: %v", err)
		}
		post(`{"tool_name":"Bash","tool_input":{"description":"list","command":"ls"},"tool_response":{}}`)
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

	it := inbox.Item{ID: "01ZERO", Run: os.Getenv("LAIGHT_RUN"), Seat: "a", Kind: inbox.Permission, Title: "t",
		Choices: []string{inbox.Allow, inbox.Deny}, Match: toolMatch(hookInput{})}
	if err := inbox.Put(home, it); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	cmdHook([]string{"post-tool-use"}, strings.NewReader(`{"tool_name":"Write","tool_input":{"content":"`+strings.Repeat("x", 300)+`"}}`), &out, &errOut)
	if _, err := inbox.Get(home, it.ID); err != nil {
		t.Errorf("an unreadable PostToolUse withdrew an item: %v", err)
	}
}

// Claude stops a hook it no longer waits for (No or Esc in the pane): the hook drops its item,
// records no answer, and denies.
func TestPermissionHookCancelled(t *testing.T) {
	home, _ := doneEnv(t)
	it, d := permission(t, home, `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, func(*inbox.Item) {
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
	cmdHook([]string{"permission-request"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`), &out, &errOut)
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
		it := inbox.Item{ID: "01" + ref, Run: id, Seat: seat, Kind: inbox.Permission, Title: "t",
			Choices: []string{inbox.Allow, inbox.Deny}, Match: toolMatch(call)}
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
