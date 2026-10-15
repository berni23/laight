package openrig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berni23/laight/internal/spec"
)

func TestImport(t *testing.T) {
	res, err := Import("testdata/openrig", "dev")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(res.Files["workflows/cycle.yaml"])
	for _, want := range []string{
		"$schema=", "id: cycle", "base: dev",
		"session: fresh", "permissions: auto", "guidance: guidance/impl.md",
		"next_hop: {on: {failed: implement, handoff: merge}}",
		"allowed_exits: [done]",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow lacks %q:\n%s", want, wf)
		}
	}
	if strings.Contains(wf, "forbid") || strings.Contains(wf, "[handoff]") {
		t.Errorf("OpenRig-only or default fields leaked:\n%s", wf)
	}
	notes := strings.Join(res.Notes, "\n")
	for _, want := range []string{"yolo", "guidance/impl.md still tells", `cwd ".." became`, "routes `failed`"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	// The result is a valid laight project.
	dir := filepath.Join(t.TempDir(), ".laight")
	for name, b := range res.Files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, probs := spec.Load(dir, filepath.Join(dir, "workflows/cycle.yaml"))
	if spec.HasErrors(probs) || w == nil {
		t.Fatalf("imported workflow doesn't validate: %v", probs)
	}
	if w.LoopGuards.MaxHops != 5 || len(w.Steps) != 3 {
		t.Errorf("max_hops %d, %d steps", w.LoopGuards.MaxHops, len(w.Steps))
	}
}

func TestImportErrors(t *testing.T) {
	if _, err := Import(t.TempDir(), "main"); err == nil {
		t.Error("no error for a folder without rig.yaml")
	}
}
