package runtime

import (
	"encoding/json"
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
	seat := &spec.Seat{Permissions: "yolo", Model: "opus"}
	got := claude{}.Command(Launch{Name: "abc123/impl", Seat: seat, SessionID: "u-1", Guidance: "/p/with space/g.md", Settings: "/s.json"})
	want := "claude --session-id u-1 --permission-mode bypassPermissions --name abc123/impl --settings /s.json --append-system-prompt-file '/p/with space/g.md' --model opus"
	if got != want {
		t.Errorf("Command =\n %s\nwant\n %s", got, want)
	}
	if s := string(claude{}.Settings("/bin/laight")); !strings.Contains(s, `"Bash(/bin/laight done:*)"`) || !strings.Contains(s, "/bin/laight hook session-start") {
		t.Errorf("Settings = %s", s)
	}
	if _, err := For("codex"); err == nil {
		t.Error("For(codex): want not supported yet")
	}
}

// Every hook the seat relies on is in its settings: without PermissionRequest, prompts sit in
// the pane; without its timeout, Claude gives up after 10 minutes; without the PostToolUse
// pair, a prompt allowed in the pane stays in the inbox.
func TestClaudeSettings(t *testing.T) {
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(claude{}.Settings("/bin/laight"), &s); err != nil {
		t.Fatal(err)
	}
	for event, want := range map[string]string{
		"SessionStart": "session-start", "Stop": "stop", "PermissionRequest": "permission-request",
		"PostToolUse": "post-tool-use", "PostToolUseFailure": "post-tool-use",
	} {
		if h := s.Hooks[event]; len(h) != 1 || len(h[0].Hooks) != 1 || h[0].Hooks[0].Command != "/bin/laight hook "+want {
			t.Errorf("%s: %+v", event, h)
		}
	}
	if h := s.Hooks["PermissionRequest"]; len(h) == 1 && h[0].Hooks[0].Timeout != 3600 {
		t.Errorf("PermissionRequest timeout %d, want 3600", h[0].Hooks[0].Timeout)
	}
}
