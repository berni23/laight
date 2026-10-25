package main

import (
	"bytes"
	"context"
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
// dialog without an inbox item, and nothing is allowed that nobody allowed. A question the agent
// asks (AskUserQuestion) reaches this hook too: it becomes a question item, and its answers go
// back as the user's (checked on Claude Code 2.1.310: allow with the tool's input plus `answers`).
func permissionHook(home, runID, seat string, in hookInput, unread error, stdout, stderr io.Writer) {
	// Claude cancels a hook it stops waiting for (No or Esc in the pane): the item goes with it,
	// since nobody would read the answer. Caught from before the item exists, so it can't be
	// left behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	decide := func(behavior, message string, input map[string]any) {
		d := map[string]any{"behavior": behavior}
		if message != "" {
			d["message"] = message
		}
		if input != nil {
			d["updatedInput"] = input
		}
		json.NewEncoder(stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PermissionRequest", "decision": d,
		}})
	}
	if unread != nil || in.ToolName == "" {
		// An item would show nothing, and an allow would allow what nobody saw.
		decide(inbox.Deny, "laight couldn't read the permission request", nil)
		return
	}
	behavior, message, input, err := askPermission(ctx, home, runID, seat, in)
	if err != nil {
		fmt.Fprintf(stderr, "laight hook: %v\n", err)
		behavior, message, input = inbox.Deny, "laight couldn't ask a human: "+err.Error(), nil
	}
	decide(behavior, message, input)
}

// askQuestion is the tool Claude asks the user questions with.
const askQuestion = "AskUserQuestion"

// askPermission raises the item and waits for its answer: allow or deny, and for a question the
// tool's input with the answers added.
func askPermission(ctx context.Context, home, runID, seat string, in hookInput) (behavior, message string, input map[string]any, err error) {
	r, err := state.Load(home, runID)
	if err != nil {
		return "", "", nil, err
	}
	now := time.Now().UTC()
	expires := now.Add(permissionWait)
	it := inbox.Item{
		ID: state.NewID(now), Run: r.ID, Repo: r.Repo, Workflow: r.Workflow, Seat: seat,
		Kind: inbox.Permission, Title: fmt.Sprintf("%s wants to use %s", seat, in.ToolName),
		Detail: permissionDetail(in), Choices: []string{inbox.Allow, inbox.Deny},
		Created: now, Expires: &expires, Match: toolMatch(in),
	}
	if qs := questions(in); len(qs) > 0 {
		it.Kind, it.Title, it.Detail, it.Choices, it.Questions = inbox.Question, questionTitle(seat, qs), "", nil, qs
	}
	if h := r.ActiveOn(seat); h != nil {
		it.Step, it.Hop = h.Step, h.N
	}
	if err := inbox.Put(home, it); err != nil {
		return "", "", nil, err
	}
	// The run may have ended since it was read, after its items were withdrawn: nobody would
	// withdraw this one. Ending records the status first, so this read sees it.
	if r, err := state.Load(home, runID); err != nil || !r.Live() {
		inbox.Drop(home, it.ID)
		return inbox.Deny, "the run ended", nil, nil
	}
	state.Append(home, state.Event{Run: r.ID, Type: "inbox.raised", Step: it.Step, Seat: seat, Hop: it.Hop,
		Data: map[string]any{"item": it.ID, "kind": it.Kind, "tool": in.ToolName}})

	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		done, err := inbox.Answered(home, it.ID)
		switch {
		case err != nil:
			return inbox.Deny, "laight couldn't read the answer", nil, nil
		case done != nil:
			return answered(done, in)
		}
		if _, err := inbox.Get(home, it.ID); errors.Is(err, os.ErrNotExist) {
			if done, _ := inbox.Answered(home, it.ID); done != nil {
				return answered(done, in) // answered between the two reads
			}
			return inbox.Deny, "withdrawn: answered in the pane, or the run ended", nil, nil
		}
		if time.Now().After(expires) {
			// Expired: deny, recorded as the answer unless one just won the race, so a late
			// answer hears it was late.
			done, err := expire(home, it)
			if err != nil && !errors.Is(err, inbox.ErrAlreadyAnswered) {
				return inbox.Deny, "nobody answered in time", nil, nil
			}
			if err == nil {
				state.Append(home, state.Event{Run: r.ID, Type: "inbox.answered", Step: it.Step, Seat: seat, Hop: it.Hop,
					Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": inbox.Deny, "expired": true}})
				return inbox.Deny, fmt.Sprintf("nobody answered within %s", permissionWait), nil, nil
			}
			return answered(done, in)
		}
		select {
		case <-ctx.Done():
			inbox.Drop(home, it.ID)
			return inbox.Deny, "cancelled", nil, nil
		case <-tick.C:
		}
	}
}

// expire records that nobody answered it in time: deny, or for a question, that nobody answered.
func expire(home string, it inbox.Item) (*inbox.Item, error) {
	if it.Kind != inbox.Question {
		return inbox.Answer(home, it.ID, inbox.Deny)
	}
	none := make([]string, len(it.Questions))
	for i := range none {
		none[i] = "(" + inbox.Deny + ": nobody answered)"
	}
	return inbox.Reply(home, it.ID, none)
}

// questions reads what an AskUserQuestion call asks; nothing for any other tool, or a call it
// can't read (which then asks for permission, showing its input).
func questions(in hookInput) []inbox.Ask {
	if in.ToolName != askQuestion {
		return nil
	}
	var v struct {
		Questions []inbox.Ask `json:"questions"`
	}
	if json.Unmarshal(in.ToolInput, &v) != nil {
		return nil
	}
	for _, q := range v.Questions {
		if strings.TrimSpace(q.Question) == "" {
			return nil
		}
	}
	return v.Questions
}

// questionTitle is the question itself, or how many there are and the first.
func questionTitle(seat string, qs []inbox.Ask) string {
	if len(qs) == 1 {
		return seat + " asks: " + qs[0].Question
	}
	return fmt.Sprintf("%s asks %d questions: %s …", seat, len(qs), qs[0].Question)
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
		if it.Run == runID && it.Seat == seat && (it.Kind == inbox.Permission || it.Kind == inbox.Question) && it.Match == m {
			inbox.Drop(home, it.ID)
			state.Append(home, state.Event{Run: runID, Type: "inbox.answered", Step: it.Step, Seat: seat, Hop: it.Hop,
				Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": "in the pane"}})
			return
		}
	}
}

// toolMatch is what PermissionRequest and PostToolUse share about one tool call (neither sends
// a tool_use_id to both): the tool and its input, re-encoded so key order doesn't matter. An
// AskUserQuestion's input gains the answers by PostToolUse, which don't count.
func toolMatch(in hookInput) string {
	var v any
	d := json.NewDecoder(bytes.NewReader(in.ToolInput))
	d.UseNumber()
	d.Decode(&v)
	if m, ok := v.(map[string]any); ok && in.ToolName == askQuestion {
		delete(m, "answers")
		delete(m, "annotations")
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte(in.ToolName+"\x00"), b...))
	return hex.EncodeToString(sum[:16])
}

func answered(it *inbox.Item, in hookInput) (string, string, map[string]any, error) {
	if it.Kind == inbox.Question {
		return replied(it, in)
	}
	if it.Answer == inbox.Allow {
		return inbox.Allow, "", nil, nil
	}
	return inbox.Deny, "denied from the laight inbox", nil, nil
}

// replied gives Claude a question's answers as the user's: the tool's own input, plus `answers`
// keyed by each question as the agent wrote it. An answer that is an option's label, as the
// inbox shows it (redacted), becomes that option's label as the agent wrote it; anything else
// goes as typed.
func replied(it *inbox.Item, in hookInput) (string, string, map[string]any, error) {
	var input map[string]any
	d := json.NewDecoder(bytes.NewReader(in.ToolInput))
	d.UseNumber()
	if err := d.Decode(&input); err != nil {
		return inbox.Deny, "laight couldn't read the question it was answering", nil, nil
	}
	asked := questions(in)
	if len(asked) != len(it.Questions) || len(it.Answers) != len(asked) {
		return inbox.Deny, "laight couldn't match the answers to the questions", nil, nil
	}
	answers := map[string]any{}
	for i, q := range asked {
		answers[q.Question] = original(it.Answers[i], it.Questions[i], q)
	}
	input["answers"] = answers
	return inbox.Allow, "", input, nil
}

// original maps an answer given from the inbox's copy of a question back to the agent's labels.
// A multi-select answer is labels joined by ", ".
func original(answer string, shown, asked inbox.Ask) string {
	label := func(a string) (string, bool) {
		for k, o := range shown.Options {
			if o.Label == a && k < len(asked.Options) {
				return asked.Options[k].Label, true
			}
		}
		return a, false
	}
	if l, ok := label(answer); ok || !shown.MultiSelect {
		return l
	}
	parts := strings.Split(answer, ", ")
	for i, p := range parts {
		parts[i], _ = label(p)
	}
	return strings.Join(parts, ", ")
}

// permissionDetail is what the tool wants to do: a Bash command as it is, anything else as its
// input, compact. The inbox redacts it and cuts it short.
func permissionDetail(in hookInput) string {
	var bash struct {
		Command string `json:"command"`
	}
	if in.ToolName == "Bash" && json.Unmarshal(in.ToolInput, &bash) == nil && bash.Command != "" {
		return bash.Command
	}
	var b bytes.Buffer
	if json.Compact(&b, in.ToolInput) != nil {
		return ""
	}
	return b.String()
}
