package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/runtime"
	"github.com/berni23/laight/internal/state"
)

// permissionWait is how long a permission item waits for an answer before laight denies it. It
// is under the hook's own timeout (runtime.PermissionTimeout), so the answer is always laight's:
// a hook Claude gives up on shows Claude's dialog in a pane nobody watches.
var (
	permissionWait = runtime.PermissionTimeout - time.Minute
	pollEvery      = 500 * time.Millisecond
)

// permissionHook puts a permission prompt in the inbox and blocks until someone answers it, then
// gives Claude the answer. Whatever goes wrong, it denies: an agent is never left waiting on a
// dialog without an inbox item, and nothing is allowed that nobody allowed.
func permissionHook(home, runID, seat string, in hookInput, stdout, stderr io.Writer) {
	decide := func(behavior, message string) {
		d := map[string]any{"behavior": behavior}
		if message != "" {
			d["message"] = message
		}
		json.NewEncoder(stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest", "decision": d,
		}})
	}
	behavior, message, err := askPermission(home, runID, seat, in)
	if err != nil {
		fmt.Fprintf(stderr, "laight hook: %v\n", err)
		behavior, message = inbox.Deny, "laight couldn't ask a human: "+err.Error()
	}
	decide(behavior, message)
}

func askPermission(home, runID, seat string, in hookInput) (behavior, message string, err error) {
	r, err := state.Load(home, runID)
	if err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	expires := now.Add(permissionWait)
	tool := in.ToolName
	if tool == "" {
		tool = "a tool"
	}
	it := inbox.Item{
		ID: state.NewID(now), Run: r.ID, Repo: r.Repo, Workflow: r.Workflow, Seat: seat,
		Kind: inbox.Permission, Title: fmt.Sprintf("%s wants to use %s", seat, tool),
		Detail: permissionDetail(in), Choices: []string{inbox.Allow, inbox.Deny},
		Created: now, Expires: &expires, Secrets: rig.SeatSecrets(), Match: toolMatch(in),
	}
	if h := r.ActiveOn(seat); h != nil {
		it.Step, it.Hop = h.Step, h.N
	}
	if err := inbox.Put(home, it); err != nil {
		return "", "", err
	}
	state.Append(home, state.Event{Run: r.ID, Type: "inbox.raised", Step: it.Step, Seat: seat, Hop: it.Hop,
		Data: map[string]any{"item": it.ID, "kind": it.Kind, "tool": in.ToolName}})

	// Claude cancels a hook it stops waiting for (the user pressed Esc in the pane): the item
	// goes with it, since nobody would read the answer.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		done, err := inbox.Answered(home, it.ID)
		switch {
		case err != nil:
			return inbox.Deny, "laight couldn't read the answer", nil
		case done != nil:
			return answered(done)
		}
		if _, err := inbox.Get(home, it.ID); errors.Is(err, os.ErrNotExist) {
			if done, _ := inbox.Answered(home, it.ID); done != nil {
				return answered(done) // answered between the two reads
			}
			return inbox.Deny, "withdrawn: answered in the pane, or the run ended", nil
		}
		if time.Now().After(expires) {
			// Expired: deny, through Answer, so an answer that just won the race still stands.
			done, err := inbox.Answer(home, it.ID, inbox.Deny)
			if err != nil && !errors.Is(err, inbox.ErrAlreadyAnswered) {
				return inbox.Deny, "nobody answered in time", nil
			}
			if err == nil {
				state.Append(home, state.Event{Run: r.ID, Type: "inbox.answered", Step: it.Step, Seat: seat, Hop: it.Hop,
					Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": inbox.Deny, "expired": true}})
				return inbox.Deny, fmt.Sprintf("nobody answered within %s", permissionWait), nil
			}
			return answered(done)
		}
		select {
		case <-sigs:
			inbox.Drop(home, it.ID)
			return inbox.Deny, "cancelled", nil
		case <-tick.C:
		}
	}
}

// answeredInPane runs after every tool call of the seat. Claude shows its own dialog while the
// PermissionRequest hook waits, and when someone allows the tool there it no longer waits for
// the hook, nor stops it: the item is withdrawn here, so nobody answers a prompt that is gone
// (the hook sees it withdrawn and returns). A dialog denied or cancelled in the pane interrupts
// the turn, and Claude stops the hook itself.
func answeredInPane(home, runID, seat string, in hookInput) {
	m := toolMatch(in)
	items, _ := inbox.Pending(home)
	for _, it := range items { // oldest first: the same command asked twice is answered in order
		if it.Run == runID && it.Seat == seat && it.Kind == inbox.Permission && it.Match == m {
			inbox.Drop(home, it.ID)
			state.Append(home, state.Event{Run: runID, Type: "inbox.answered", Step: it.Step, Seat: seat, Hop: it.Hop,
				Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": "in the pane"}})
			return
		}
	}
}

// toolMatch is what PermissionRequest and PostToolUse share about one tool call (neither sends
// a tool_use_id to both): the tool and its input, re-encoded so key order doesn't matter.
func toolMatch(in hookInput) string {
	var v any
	d := json.NewDecoder(bytes.NewReader(in.ToolInput))
	d.UseNumber()
	d.Decode(&v)
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte(in.ToolName+"\x00"), b...))
	return hex.EncodeToString(sum[:16])
}

func answered(it *inbox.Item) (string, string, error) {
	if it.Answer == inbox.Allow {
		return inbox.Allow, "", nil
	}
	return inbox.Deny, "denied from the laight inbox", nil
}

// permissionDetail is what the tool wants to do: a Bash command as it is, anything else as its
// input, compact and cut short. The inbox redacts it.
func permissionDetail(in hookInput) string {
	var bash struct {
		Command string `json:"command"`
	}
	if in.ToolName == "Bash" && json.Unmarshal(in.ToolInput, &bash) == nil && bash.Command != "" {
		return cut(bash.Command)
	}
	var b bytes.Buffer
	if json.Compact(&b, in.ToolInput) != nil {
		return ""
	}
	return cut(b.String())
}

func cut(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "…"
}
