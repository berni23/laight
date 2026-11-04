package runtime

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/berni23/laight/internal/spec"
)

func TestChoose(t *testing.T) {
	accept := claude{}.Accept()
	trustNo := " Quick safety check: Is this a project you created or one you trust?\n ❯ No, exit\n   Yes, I trust this folder\n"
	trustYes := " Quick safety check: Is this a project you created or one you trust?\n   No, exit\n ❯ Yes, I trust this folder\n"
	ready := "❯ [⧉ In README.md] Try \"how does <filepath> work?\"\n  ⏸ manual mode on"
	for _, tt := range []struct{ screen, want string }{
		{trustNo, "Down"},
		{trustYes, "Enter"},
		{ready, ""},
		{"  No, exit\n❯ Yes, I accept\n", "Enter"},
	} {
		if got := Choose(tt.screen, accept); got != tt.want {
			t.Errorf("Choose(%q) = %q, want %q", tt.screen, got, tt.want)
		}
	}
}

func TestClaudeCommand(t *testing.T) {
	seat := &spec.Seat{Permissions: spec.Permissions{Mode: "yolo"}, Model: "opus"}
	got := claude{}.Command(Launch{Name: "abc123/impl", Seat: seat, SessionID: "u-1", Guidance: "/p/with space/g.md", Settings: "/s.json"})
	want := "claude --session-id u-1 --permission-mode bypassPermissions --name abc123/impl --settings /s.json --append-system-prompt-file '/p/with space/g.md' --model opus"
	if got != want {
		t.Errorf("Command =\n %s\nwant\n %s", got, want)
	}
	if s := string(claude{}.Settings("/bin/laight", nil)); !strings.Contains(s, `"Bash(/bin/laight done:*)"`) || !strings.Contains(s, `"Bash(laight done:*)"`) || !strings.Contains(s, "/bin/laight hook session-start") {
		t.Errorf("Settings = %s", s)
	}
	if _, err := For("codex"); err == nil {
		t.Error("For(codex): want not supported yet")
	}
}

// Every hook the seat relies on is in its settings: without PermissionRequest, prompts sit in
// the pane; without its timeout, Claude gives up after 10 minutes; without the PostToolUse
// pair, a question answered in the pane stays in the inbox; without PermissionDenied, auto
// mode's refusals are missing from the run. The deny rules go in as given, after laight's own
// allow rules.
func TestClaudeSettings(t *testing.T) {
	var s struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	deny := []string{"Bash(git push --force:*)", "mcp__github"}
	if err := json.Unmarshal(claude{}.Settings("/bin/laight", deny), &s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.Permissions.Deny, deny) || len(s.Permissions.Allow) != 2 {
		t.Errorf("permissions: %+v", s.Permissions)
	}
	for event, want := range map[string]string{
		"SessionStart": "session-start", "Stop": "stop", "PermissionRequest": "permission-request",
		"PostToolUse": "post-tool-use", "PostToolUseFailure": "post-tool-use", "PermissionDenied": "permission-denied",
	} {
		if h := s.Hooks[event]; len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "/bin/laight hook "+want {
			t.Errorf("%s: %+v", event, h)
		}
	}
	if h := s.Hooks["PermissionRequest"]; len(h) == 1 && h[0].Hooks[0].Timeout != 3600 {
		t.Errorf("PermissionRequest timeout %d, want 3600", h[0].Hooks[0].Timeout)
	}
}
