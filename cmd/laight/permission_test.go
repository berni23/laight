package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
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
