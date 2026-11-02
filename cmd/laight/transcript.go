package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/berni23/laight/internal/inbox"
)

// contextMax is how much of what the agent said before asking a question its item keeps: the
// end of it, so the inbox shows what led to the question (the queue cuts a detail at 2000 bytes).
const contextMax = 1500

// transcriptTail is how far back in the transcript laight looks for the turn: transcripts grow
// to megabytes, and only the end of the current turn is kept anyway.
const transcriptTail = 1 << 20

// turnContext is what the agent wrote in its current turn before calling AskUserQuestion: its
// text, and one line per tool it ran, read from Claude's transcript (a JSONL file whose path
// every hook gets), then what it said last, read from its screen (said). The prompt that started
// the turn, thinking and tool results are left out. Empty when neither can be read: the question
// is still asked, with less context.
//
// Claude writes the question's own message to the transcript only once the question is answered
// (seen on 2.1.314), and that message holds what the agent said just before asking: the screen
// still shows it, above the question's dialog.
func turnContext(path, said string) string {
	turn, asked, _ := readTurn(path)
	if asked {
		turn = turn[:len(turn)-1] // the item shows the question already
	}
	if said != "" && (len(turn) == 0 || strings.Join(strings.Fields(turn[len(turn)-1]), " ") != strings.Join(strings.Fields(said), " ")) {
		turn = append(turn, said)
	}
	return lastBytes(inbox.Scrub(strings.TrimSpace(strings.Join(turn, "\n"))), contextMax) // scrub, then cut
}

// lastSaid is the agent's last message on a Claude screen showing a question: the last block
// that starts with ⏺, up to the dialog's rule. Empty when there is none, or it is a tool call
// (the transcript has those).
func lastSaid(screen string) string {
	lines := strings.Split(screen, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "⏺ ") {
			start = i
		}
	}
	if start < 0 || toolCall.MatchString(lines[start]) {
		return ""
	}
	var said []string
	for _, l := range lines[start:] {
		if t := strings.TrimSpace(l); len(t) >= 20 && strings.Trim(t, "─") == "" {
			break
		}
		said = append(said, strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(l, "⏺ "), "  "), " "))
	}
	return strings.TrimSpace(strings.Join(said, "\n"))
}

// toolCall is how Claude shows a tool call: ⏺ Bash(ls), ⏺ mcp__x__y(…).
var toolCall = regexp.MustCompile(`^⏺ [\w:.-]+\(`)

// readTurn reads the transcript's current turn, and whether it ends with the question being
// asked, which means Claude has written the turn up to the hook.
func readTurn(path string) (turn []string, asked bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	partial := false
	if fi, err := f.Stat(); err == nil && fi.Size() > transcriptTail {
		f.Seek(-transcriptTail, io.SeekEnd)
		partial = true
	}
	r := bufio.NewReader(f)
	if partial {
		r.ReadBytes('\n') // the first line is cut
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			turn = readEntry(line, turn)
		}
		if err != nil {
			break
		}
	}
	n := len(turn)
	return turn, n > 0 && strings.HasPrefix(turn[n-1], "  ran: "+askQuestion), nil
}

// entry is the part of a transcript line laight reads.
type entry struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// readEntry adds one transcript line to the turn so far: a prompt starts a new turn, the agent's
// text and tool calls are added to it, anything else is skipped.
func readEntry(line []byte, turn []string) []string {
	var e entry
	if json.Unmarshal(line, &e) != nil || e.IsMeta {
		return turn
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		if e.Type == "user" {
			return nil // a prompt as a string
		}
		return turn
	}
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return turn
	}
	switch e.Type {
	case "user":
		// A prompt, unless it carries tool results (Claude's answers to the agent's own calls).
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return turn
			}
		}
		if len(blocks) > 0 {
			return nil
		}
	case "assistant":
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					turn = append(turn, t)
				}
			case "tool_use":
				turn = append(turn, strings.TrimRight("  ran: "+b.Name+"  "+toolGist(b.Name, b.Input), " "))
			}
		}
	}
	return turn
}

// toolGist is the one thing that says what a tool call did: a command's first line, a file, a
// pattern or a URL; nothing for a tool laight doesn't know.
func toolGist(name string, input json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(input, &in) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "notebook_path", "pattern", "url", "description"} {
		if s, ok := in[k].(string); ok && s != "" {
			first, _, more := strings.Cut(inbox.Scrub(s), "\n") // scrub, then cut
			if more {
				first += " …"
			}
			if len(first) > 120 {
				first = strings.ToValidUTF8(first[:120], "") + " …"
			}
			return first
		}
	}
	return ""
}

// lastBytes keeps the end of s, at most n bytes, from the start of a line when one is near, and
// marks the cut.
func lastBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, '\n'); i >= 0 && i < n/4 {
		s = s[i+1:]
	}
	return "… " + strings.ToValidUTF8(s, "")
}
