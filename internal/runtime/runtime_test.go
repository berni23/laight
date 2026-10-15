package runtime

import (
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
