// Package runtime adapts agent CLIs (Claude Code, a plain shell command; Codex later) to the
// driver: how to launch one, whether it reports ready through hooks, and which launch prompts the
// driver may accept on its own.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/tmux"
)

// Launch is what an adapter needs to start one seat.
type Launch struct {
	Name      string     // a display name for the session, e.g. "7k3mqz/impl"
	Seat      *spec.Seat //
	SessionID string     // a UUID the runtime should use, so it can be resumed later
	Resume    bool       // SessionID ran before: continue that conversation
	Guidance  string     // absolute path of the guidance file, or ""
	Settings  string     // absolute path of the generated settings file, or ""
}

// Runtime is one agent CLI.
type Runtime interface {
	// Command is the shell command line that starts the seat.
	Command(l Launch) string
	// Settings is the per-seat settings file to generate (hooks, permissions), or nil.
	Settings(laight string) []byte
	// Hooks reports whether the runtime announces ready through the SessionStart hook.
	Hooks() bool
	// Accept is the launch prompts the driver may answer, as the text of the option to pick.
	Accept() []string
}

// For returns the adapter for a seat's runtime.
func For(name string) (Runtime, error) {
	switch name {
	case "claude-code":
		return claude{}, nil
	case "shell":
		return shell{}, nil
	}
	return nil, fmt.Errorf("runtime %q is not supported yet", name)
}

type claude struct{}

// permissionModes maps a seat's `permissions` to Claude's --permission-mode.
var permissionModes = map[string]string{"default": "manual", "auto": "auto", "yolo": "bypassPermissions"}

func (claude) Command(l Launch) string {
	argv := []string{"claude", "--session-id", l.SessionID}
	if l.Resume && transcriptExists(l.SessionID) {
		argv = []string{"claude", "--resume", l.SessionID}
	}
	argv = append(argv, "--permission-mode", permissionModes[l.Seat.Permissions])
	if l.Name != "" {
		argv = append(argv, "--name", l.Name)
	}
	if l.Settings != "" {
		argv = append(argv, "--settings", l.Settings)
	}
	if l.Guidance != "" {
		argv = append(argv, "--append-system-prompt-file", l.Guidance)
	}
	if l.Seat.Model != "" {
		argv = append(argv, "--model", l.Seat.Model)
	}
	return tmux.Join(argv)
}

// transcriptExists reports whether Claude saved a conversation for id: it does so only once the
// session gets its first message, and --resume fails without one.
func transcriptExists(id string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
	return len(m) > 0
}

// Settings lets the seat finish steps without a prompt, reports ready through SessionStart and
// the end of each turn through Stop.
// Passed with --settings, so the user's own ~/.claude/settings.json is never written.
func (claude) Settings(laight string) []byte {
	hook := func(event string) []any {
		return []any{map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": tmux.Quote(laight) + " hook " + event,
		}}}}
	}
	b, _ := json.MarshalIndent(map[string]any{
		"permissions": map[string]any{"allow": []string{"Bash(" + laight + " done:*)", "Bash(laight done:*)"}},
		"hooks":       map[string]any{"SessionStart": hook("session-start"), "Stop": hook("stop")},
	}, "", "  ")
	return b
}

func (claude) Hooks() bool { return true }

// Accept answers the folder-trust dialog of a new worktree and the bypass-permissions warning.
func (claude) Accept() []string { return []string{"Yes, I trust this folder", "Yes, I accept"} }

type shell struct{}

func (shell) Command(l Launch) string { return l.Seat.Command }
func (shell) Settings(string) []byte  { return nil }
func (shell) Hooks() bool             { return false }
func (shell) Accept() []string        { return nil }

// Choose looks for a launch prompt on screen offering one of the accept options. It returns the
// key to press next: "Enter" when the cursor (❯) is on the option, "Down" to move towards it,
// or "" when no such prompt is on screen.
func Choose(screen string, accept []string) string {
	for _, opt := range accept {
		if !strings.Contains(screen, opt) {
			continue
		}
		for _, line := range strings.Split(screen, "\n") {
			if strings.Contains(line, "❯") && strings.Contains(line, opt) {
				return "Enter"
			}
		}
		return "Down"
	}
	return ""
}
