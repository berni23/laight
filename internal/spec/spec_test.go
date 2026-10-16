package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// issueCycle is the acceptance workflow of CLAUDE.md section 3.
const issueCycle = `version: 1
id: issue-cycle
summary: One issue, implemented and independently reviewed, merged into {{ .base }}.
base: dev
inputs:
  issue: { required: false, description: Issue number }
seats:
  impl:
    label: Implementer
    runtime: claude-code
    cwd: worktree
    session: resume
    permissions: auto
    guidance: guidance/implementer.md
    model: opus
    env: { GH_TOKEN: "{{ secret \"gh\" }}" }
  reviewer:
    uses: seats/reviewer.yaml
    session: fresh
steps:
  - id: implement
    seat: impl
    objective: |
      Implement issue {{ .inputs.issue }} from {{ .base }}. laight done handoff pr=<URL> issue=<N>
    allowed_exits: [handoff]
    next_hop: { on: { handoff: review } }
  - id: review
    seat: reviewer
    objective: Review {{ .steps.implement.out.pr }}.
    next_hop: { on: { handoff: fix } }
  - id: fix
    seat: impl
    objective: Address every finding on {{ .steps.implement.out.pr }}.
    next_hop: { on: { handoff: verify } }
  - id: verify
    seat: reviewer
    objective: Verify every finding.
    allowed_exits: [pass, fail]
    next_hop: { on: { pass: approve, fail: fix } }
  - id: approve
    type: gate
    ask: Merge {{ .steps.implement.out.pr }} into {{ .base }}?
    allowed_exits: [yes, no]
    next_hop: { on: { yes: merge, no: end } }
  - id: merge
    type: shell
    run: gh pr merge {{ .steps.implement.out.pr }} --squash
loop_guards: { max_hops: 11 }
timeouts: { step: 90m }
on_finish: { success: destroy, failure: keep }
`

const reviewerSeat = `label: Reviewer
runtime: claude-code
session: resume
permissions: auto
guidance: guidance/reviewer.md
`

// project writes a .laight folder with the given workflow and the shared files it uses.
func project(t *testing.T, workflow string) (dir, file string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), DirName)
	files := map[string]string{
		"workflows/issue-cycle.yaml": workflow,
		"seats/reviewer.yaml":        reviewerSeat,
		"guidance/implementer.md":    "# implementer\n",
		"guidance/reviewer.md":       "# reviewer\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "workflows/issue-cycle.yaml")
}

func TestLoadIssueCycle(t *testing.T) {
	dir, file := project(t, issueCycle)
	w, probs := Load(dir, file)
	for _, p := range probs {
		t.Errorf("unexpected problem: %s", p)
	}
	if w == nil {
		t.Fatal("no workflow")
	}
	r := w.Seats["reviewer"]
	if r.Session != "fresh" || r.Label != "Reviewer" || r.Cwd != "worktree" {
		t.Errorf("reviewer seat = %+v, want uses: merged, inline session fresh, default cwd worktree", r)
	}
	if got := w.Step("review").AllowedExits; len(got) != 1 || got[0] != "handoff" {
		t.Errorf("review default exits = %v, want [handoff]", got)
	}
	if got := w.Step("merge").AllowedExits; len(got) != 1 || got[0] != "ok" {
		t.Errorf("merge default exits = %v, want [ok]", got)
	}
	if w.Step("approve").Type != TypeGate || w.Step("merge").Type != TypeShell || w.Step("fix").Type != TypeAgent {
		t.Error("step types not inferred")
	}
	if got := time.Duration(w.Step("fix").Timeout); got != 90*time.Minute {
		t.Errorf("fix timeout = %v, want the workflow's 90m", got)
	}
	if w.Seats["impl"].Env["GH_TOKEN"] == "" {
		t.Error("env lost")
	}
}

func TestLoadProblems(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(string) string
		want    string // substring of one problem
		line    int    // 0: don't check
		warning bool
	}{
		{"bad yaml", func(s string) string { return s + "\n  - : :\n\t" }, "not valid YAML", 0, false},
		{"schema: unknown field", func(s string) string { return strings.Replace(s, "base: dev", "base: dev\nrig: gorg", 1) }, "rig", 5, false},
		{"schema: bad permissions", func(s string) string { return strings.Replace(s, "permissions: auto", "permissions: maybe", 1) }, "permissions", 13, false},
		{"schema: bad duration", func(s string) string { return strings.Replace(s, "step: 90m", "step: soon", 1) }, "timeouts", 0, false},
		{"unknown seat", func(s string) string {
			return strings.Replace(s, "seat: impl\n    objective: |", "seat: imp\n    objective: |", 1)
		}, `unknown seat "imp"`, 22, false},
		{"unknown route target", func(s string) string { return strings.Replace(s, "pass: approve", "pass: aprove", 1) }, `unknown step "aprove"`, 0, false},
		{"route exit not allowed", func(s string) string { return strings.Replace(s, "on: { handoff: fix }", "on: { done: fix }", 1) }, `exit "done" is not in allowed_exits`, 0, false},
		{"unreachable step", func(s string) string { return strings.Replace(s, "yes: merge", "yes: end", 1) }, "", 0, false}, // merge becomes an entry step: no error
		{"duplicate id", func(s string) string { return strings.Replace(s, "id: fix", "id: review", 1) }, "already used", 0, false},
		{"missing guidance", func(s string) string { return strings.Replace(s, "guidance/implementer.md", "guidance/nope.md", 1) }, "guidance file guidance/nope.md not found", 0, false},
		{"missing uses file", func(s string) string { return strings.Replace(s, "seats/reviewer.yaml", "seats/nope.yaml", 1) }, "can't read seats/nope.yaml", 0, false},
		{"unknown template field", func(s string) string {
			return strings.Replace(s, ".steps.implement.out.pr }}.\n", ".steps.implemnt.out.pr }}.\n", 1)
		}, `unknown step "implemnt"`, 0, false},
		{"undeclared input", func(s string) string { return strings.Replace(s, ".inputs.issue", ".inputs.number", 1) }, `no input "number"`, 0, false},
		{"bad template", func(s string) string { return strings.Replace(s, "Review {{ .steps", "Review {{ .steps }", 1) }, "bad template", 0, false},
		{"foreign template root", func(s string) string { return strings.Replace(s, "into {{ .base }}?", "into {{ .rig.base }}?", 1) }, "templates see only", 0, false},
		{"route into needs step", func(s string) string {
			return strings.Replace(s, "  - id: merge\n", "  - id: merge\n    needs: [approve]\n", 1)
		}, `step "merge" has ` + "`needs`", 0, false},
		{"field of another type", func(s string) string { return strings.Replace(s, "type: gate\n", "type: gate\n    seat: impl\n", 1) }, "`seat` doesn't belong in a gate step", 0, false},
		{"yolo warns", func(s string) string { return strings.Replace(s, "permissions: auto", "permissions: yolo", 1) }, "yolo", 13, true},
		{"cwd repo warns", func(s string) string { return strings.Replace(s, "cwd: worktree", "cwd: repo", 1) }, "share the main checkout", 0, true},
		{"unrouted exit warns", func(s string) string { return strings.Replace(s, "yes: merge, no: end", "yes: merge", 1) }, `exit "no" has no route`, 0, true},
		{"id must match file name", func(s string) string { return strings.Replace(s, "id: issue-cycle", "id: cycle", 1) }, "must match the file name", 0, false},
		{"range body sees the element", func(s string) string {
			return strings.Replace(s, "Review {{ .steps", "Review {{ range .steps.implement.out.prs }}{{ .url }}{{ $.base }}{{ end }}{{ .steps", 1)
		}, "", 0, false},
		{"range body checks $ fields", func(s string) string {
			return strings.Replace(s, "Review {{ .steps", "Review {{ with .steps.implement.out.pr }}{{ $.nope }}{{ end }}{{ .steps", 1)
		}, ".nope: templates see only", 0, false},
		{"workflow env template", func(s string) string {
			return strings.Replace(s, "base: dev", "base: dev\nenv: { X: \"{{ .nope }}\" }", 1)
		}, ".nope: templates see only", 0, false},
		{"absolute guidance", func(s string) string { return strings.Replace(s, "guidance/implementer.md", "/etc/hosts", 1) }, "must be a path inside .laight/", 0, false},
		{"seat named engine", func(s string) string {
			return strings.Replace(strings.ReplaceAll(s, "seat: impl\n", "seat: engine\n"), "  impl:\n", "  engine:\n", 1)
		}, `seat name "engine" is reserved`, 0, false},
		{"secret in an objective", func(s string) string {
			return strings.Replace(s, "Review {{ .steps", `Review {{ secret "gh" }} {{ .steps`, 1)
		}, "`secret` is only allowed in `env`", 0, false},
		{"guidance outside .laight", func(s string) string { return strings.Replace(s, "guidance/implementer.md", "../README.md", 1) }, "must be a path inside .laight/", 0, false},
		{"env on an agent step", func(s string) string {
			return strings.Replace(s, "objective: Verify every finding.\n", "objective: Verify every finding.\n    env: { X: y }\n", 1)
		}, "`env` is only for shell steps", 0, false},
		{"env on a gate", func(s string) string { return strings.Replace(s, "type: gate\n", "type: gate\n    env: { X: y }\n", 1) }, "`env` is only for shell steps", 0, false},
		{"secret in shell env", func(s string) string {
			return strings.Replace(s, "--squash\n", "--squash\n    env: { GH_TOKEN: '{{ secret \"gh\" }}' }\n", 1)
		}, "", 0, false},
		{"secret in run", func(s string) string { return strings.Replace(s, "--squash", `--squash {{ secret "gh" }}`, 1) }, "`secret` is only allowed in `env`", 0, false},
		{"shell exit not allowed", func(s string) string {
			return strings.Replace(s, "--squash\n", "--squash\n    exits: { \"3\": retry }\n    allowed_exits: [ok]\n", 1)
		}, `exit "retry" is not in allowed_exits`, 0, false},
		{"shell exit code with a leading zero", func(s string) string {
			return strings.Replace(s, "--squash\n", "--squash\n    exits: { \"03\": retry }\n    next_hop: { on: { retry: merge, ok: end } }\n", 1)
		}, `'03' does not match pattern`, 0, false},
		{"shell exit code out of range", func(s string) string {
			return strings.Replace(s, "--squash\n", "--squash\n    exits: { \"256\": retry }\n    next_hop: { on: { retry: merge, ok: end } }\n", 1)
		}, `'256' does not match pattern`, 0, false},
		{"shell exits default allowed", func(s string) string {
			return strings.Replace(s, "--squash\n", "--squash\n    exits: { \"3\": retry }\n    next_hop: { on: { retry: merge, ok: end } }\n", 1)
		}, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, file := project(t, tt.edit(issueCycle))
			_, probs := Load(dir, file)
			if tt.want == "" {
				if HasErrors(probs) {
					t.Errorf("unexpected errors: %v", probs)
				}
				return
			}
			for _, p := range probs {
				if strings.Contains(p.String(), tt.want) {
					if (p.Severity == Warning) != tt.warning {
						t.Errorf("%s: severity %s", p, p.Severity)
					}
					if tt.line != 0 && p.Line != tt.line {
						t.Errorf("%s: line %d, want %d", p, p.Line, tt.line)
					}
					return
				}
			}
			t.Errorf("no problem containing %q; got %v", tt.want, probs)
		})
	}
}

const parallel = `version: 1
id: issue-cycle
seats:
  lead: { runtime: shell, command: bash }
  dev: { runtime: shell, command: bash }
steps:
  - id: plan
    seat: lead
    objective: plan
  - id: build
    needs: [plan]
    matrix: { task: "{{ .steps.plan.out.tasks }}" }
    seat: dev
    objective: Implement {{ .matrix.task }}
  - id: integrate
    needs: [build]
    seat: lead
    objective: integrate
`

func TestParallel(t *testing.T) {
	dir, file := project(t, parallel)
	w, probs := Load(dir, file)
	if HasErrors(probs) {
		t.Fatalf("problems: %v", probs)
	}
	if got := w.Step("build").MaxParallel; got != DefaultMaxParallel {
		t.Errorf("max_parallel = %d, want default %d", got, DefaultMaxParallel)
	}

	bad := map[string]string{
		"needs cycle":         strings.Replace(parallel, "needs: [plan]", "needs: [plan, integrate]", 1),
		"matrix step routes":  strings.Replace(parallel, "objective: Implement {{ .matrix.task }}", "objective: x\n    next_hop: { on: { handoff: integrate } }", 1),
		"matrix outside":      strings.Replace(parallel, "objective: integrate", "objective: '{{ .matrix.task }}'", 1),
		"unknown needs":       strings.Replace(parallel, "needs: [build]", "needs: [biuld]", 1),
		"max_parallel alone":  strings.Replace(parallel, "objective: integrate", "objective: integrate\n    max_parallel: 2", 1),
		"shell without cmd":   strings.Replace(parallel, "lead: { runtime: shell, command: bash }", "lead: { runtime: shell }", 1),
		"no entry (all need)": strings.Replace(parallel, "objective: plan", "objective: plan\n    needs: [integrate]", 1),
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			dir, file := project(t, src)
			if _, probs := Load(dir, file); !HasErrors(probs) {
				t.Errorf("no error; problems: %v", probs)
			}
		})
	}
}

func TestFindProject(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(filepath.Join(root, DirName, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DirName, "workflows", "x.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := FindProject(deep)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(root); p.Root != root && p.Root != want {
		t.Errorf("root = %s, want %s", p.Root, root)
	}
	if f, err := p.WorkflowFile("x"); err != nil || filepath.Base(f) != "x.yaml" {
		t.Errorf("WorkflowFile(x) = %q, %v", f, err)
	}
	if _, err := p.WorkflowFile("y"); err == nil || !strings.Contains(err.Error(), "workflows: x") {
		t.Errorf("WorkflowFile(y) error = %v, want it to list x", err)
	}
	if _, err := FindProject(t.TempDir()); err != ErrNoProject {
		t.Errorf("FindProject(empty) = %v, want ErrNoProject", err)
	}
}

func TestShellExitsRemapZero(t *testing.T) {
	dir, file := project(t, strings.Replace(issueCycle, "    run: gh pr merge", "    exits: { 0: merged, 1: conflict }\n    run: gh pr merge", 1))
	w, probs := Load(dir, file)
	if HasErrors(probs) {
		t.Fatal(probs)
	}
	if got := w.Step("merge").AllowedExits; strings.Join(got, ",") != "merged,conflict" {
		t.Errorf("merge exits = %v, want [merged conflict]: code 0 never gives ok", got)
	}
}

// Problems on one line (a flow-style next_hop) come out in the same order every time.
func TestProblemOrderStable(t *testing.T) {
	dir, file := project(t, strings.Replace(issueCycle, "on: { handoff: fix }", "on: { zz: fix, mm: fix, aa: fix }", 1))
	var first string
	for i := 0; i < 20; i++ {
		_, probs := Load(dir, file)
		got := fmt.Sprint(probs)
		if i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("order changed:\n%s\n%s", first, got)
		}
	}
	if a, z := strings.Index(first, `"aa"`), strings.Index(first, `"zz"`); a < 0 || a > z {
		t.Errorf("want aa before zz: %s", first)
	}
}
