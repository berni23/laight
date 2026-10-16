package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"

	"go.yaml.in/yaml/v3"
)

// TemplateFuncs are the functions templates may call. Rendering replaces them with real ones;
// for checking only the names matter.
var TemplateFuncs = template.FuncMap{"secret": func(string) string { return "" }}

// check runs the semantic checks of CLAUDE.md section 3 on a schema-valid, defaulted workflow.
func check(laightDir string, w *Workflow, root *yaml.Node) []Problem {
	c := &checker{w: w, root: root, laightDir: laightDir}
	c.workflow()
	c.seats()
	c.steps()
	if !HasErrors(c.probs) {
		c.graph()
	}
	return c.probs
}

type checker struct {
	w         *Workflow
	root      *yaml.Node
	laightDir string
	probs     []Problem
}

func (c *checker) add(sev Severity, path []string, format string, args ...any) {
	c.probs = append(c.probs, Problem{
		Severity: sev,
		File:     c.w.File,
		Line:     lineOf(c.root, path),
		Path:     path,
		Msg:      fmt.Sprintf(format, args...),
	})
}

func (c *checker) workflow() {
	if stem := strings.TrimSuffix(filepath.Base(c.w.File), filepath.Ext(c.w.File)); stem != c.w.ID {
		c.add(Error, []string{"id"}, "id %q must match the file name %q (`laight run %s` finds the workflow by its file)", c.w.ID, stem, stem)
	}
	c.template([]string{"summary"}, c.w.Summary, nil, false)
	for _, k := range sortedKeys(c.w.Env) {
		c.template([]string{"env", k}, c.w.Env[k], nil, true)
	}
}

func (c *checker) seats() {
	used := map[string]bool{}
	for _, st := range c.w.Steps {
		used[st.Seat] = true
	}
	for _, name := range sortedKeys(c.w.Seats) {
		s := c.w.Seats[name]
		at := func(field string) []string { return []string{"seats", name, field} }
		if name == "engine" {
			c.add(Error, []string{"seats", name}, "seat name %q is reserved: window 0 of every rig is the engine", name)
		}
		if s.Runtime == "shell" && s.Command == "" {
			c.add(Error, at("runtime"), "runtime shell needs a `command`")
		}
		if s.Runtime != "shell" && s.Command != "" {
			c.add(Error, at("command"), "`command` is only for runtime shell")
		}
		if s.Guidance != "" && !filepath.IsLocal(filepath.FromSlash(s.Guidance)) {
			c.add(Error, at("guidance"), "guidance %s must be a path inside .laight/, so it's versioned with the repo", s.Guidance)
		} else if s.Guidance != "" {
			if _, err := os.Stat(filepath.Join(c.laightDir, filepath.FromSlash(s.Guidance))); err != nil {
				c.add(Error, at("guidance"), "guidance file %s not found (paths are relative to .laight/)", s.Guidance)
			}
		}
		if s.Permissions == "yolo" {
			c.add(Warning, at("permissions"), "yolo: this agent runs every command unasked, with your credentials")
		}
		if s.Cwd == "repo" {
			c.add(Warning, at("cwd"), "cwd repo: two live runs of this workflow would share the main checkout")
		}
		if s.Setup != "" && s.Cwd != "worktree" {
			c.add(Warning, at("setup"), "setup only runs in a new worktree (cwd: worktree)")
		}
		for _, k := range sortedKeys(s.Env) {
			c.template([]string{"seats", name, "env", k}, s.Env[k], nil, true)
		}
		if !used[name] {
			c.add(Warning, []string{"seats", name}, "no step uses seat %q", name)
		}
	}
}

func (c *checker) steps() {
	ids := map[string]int{}
	for i, st := range c.w.Steps {
		at := func(field ...string) []string { return append([]string{"steps", strconv.Itoa(i)}, field...) }
		if j, dup := ids[st.ID]; dup {
			c.add(Error, at("id"), "step id %q is already used by steps[%d]", st.ID, j)
		}
		ids[st.ID] = i
		if st.ID == End {
			c.add(Error, at("id"), "%q is reserved: route to it to end a branch", End)
		}
		if st.Type == "" {
			c.add(Error, at(), "say what the step does: `seat` (an agent), `run` (a shell command) or `type: gate`")
			continue
		}
		fields := map[string]bool{"seat": st.Seat != "", "objective": st.Objective != "", "run": st.Run != "", "exits": len(st.Exits) > 0, "ask": st.Ask != ""}
		own := map[string][]string{TypeAgent: {"seat", "objective"}, TypeShell: {"run", "exits"}, TypeGate: {"ask"}}[st.Type]
		for _, f := range sortedKeys(fields) {
			if fields[f] && !contains(own, f) {
				c.add(Error, at(f), "`%s` doesn't belong in a %s step", f, st.Type)
			}
		}
		switch st.Type {
		case TypeAgent:
			if st.Seat == "" {
				c.add(Error, at(), "an agent step needs a `seat`")
			} else if c.w.Seats[st.Seat] == nil {
				c.add(Error, at("seat"), "unknown seat %q (seats: %s)", st.Seat, strings.Join(sortedKeys(c.w.Seats), ", "))
			}
			if st.Objective == "" {
				c.add(Error, at(), "an agent step needs an `objective`")
			}
			c.template(at("objective"), st.Objective, st, false)
		case TypeShell:
			if st.Run == "" {
				c.add(Error, at(), "a shell step needs `run`")
			}
			c.template(at("run"), st.Run, st, false)
			for _, code := range sortedKeys(st.Exits) {
				if !st.Allows(st.Exits[code]) {
					c.add(Error, at("exits", code), "exit %q is not in allowed_exits %v", st.Exits[code], st.AllowedExits)
				}
			}
		case TypeGate:
			if st.Ask == "" {
				c.add(Error, at(), "a gate step needs `ask`")
			}
			c.template(at("ask"), st.Ask, st, false)
		}
		if contains(st.AllowedExits, ExitFailed) {
			c.add(Warning, at("allowed_exits"), "`failed` is always allowed; no need to list it")
		}
		for _, exit := range sortedKeys(st.NextHop.On) {
			if !st.Allows(exit) {
				c.add(Error, at("next_hop", "on", exit), "exit %q is not in allowed_exits %v", exit, st.AllowedExits)
			}
		}
		if st.NextHop.Mode == End && len(st.NextHop.On) > 0 {
			c.add(Error, at("next_hop"), "`mode: end` and `on` contradict each other")
		}
		if len(st.NextHop.On) > 0 {
			for _, e := range st.AllowedExits {
				if _, ok := st.NextHop.On[e]; !ok {
					c.add(Warning, at("next_hop", "on"), "exit %q has no route, so it ends the branch (route it to `end` to say so)", e)
				}
			}
		}
		if st.MaxParallel > 0 && len(st.Matrix) == 0 {
			c.add(Error, at("max_parallel"), "max_parallel only applies to a matrix step")
		}
		for _, k := range sortedKeys(st.Matrix) {
			if s, ok := st.Matrix[k].(string); ok {
				c.template(at("matrix", k), s, st, false)
			}
		}
		if len(st.Env) > 0 && st.Type != TypeShell {
			c.add(Error, at("env"), "`env` is only for shell steps: an agent's env is its seat's (seats.<name>.env), fixed when it starts")
		}
		for _, k := range sortedKeys(st.Env) {
			c.template(at("env", k), st.Env[k], st, true)
		}
	}
}

// graph checks the scheduling rules: references, the needs/route separation (rule 6), needs
// cycles and reachability.
func (c *checker) graph() {
	index := map[string]int{}
	for i, st := range c.w.Steps {
		index[st.ID] = i
	}
	routedTo := map[string]bool{}
	for i, st := range c.w.Steps {
		at := func(field ...string) []string { return append([]string{"steps", strconv.Itoa(i)}, field...) }
		for _, exit := range sortedKeys(st.NextHop.On) {
			target := st.NextHop.On[exit]
			if target == End {
				continue
			}
			j, ok := index[target]
			switch {
			case !ok:
				c.add(Error, at("next_hop", "on", exit), "unknown step %q", target)
			case len(c.w.Steps[j].Needs) > 0:
				c.add(Error, at("next_hop", "on", exit), "step %q has `needs`, so it can't also be a route target", target)
			default:
				routedTo[target] = true
			}
		}
		if len(st.Matrix) > 0 && (len(st.NextHop.On) > 0 || st.NextHop.Mode != "") {
			c.add(Error, at("next_hop"), "a matrix step can't route; later steps join it with `needs`")
		}
		for k, n := range st.Needs {
			if n == st.ID {
				c.add(Error, at("needs", strconv.Itoa(k)), "a step can't need itself")
			} else if _, ok := index[n]; !ok {
				c.add(Error, at("needs", strconv.Itoa(k)), "unknown step %q", n)
			}
		}
	}
	if HasErrors(c.probs) {
		return
	}
	if cycle := needsCycle(c.w.Steps); cycle != nil {
		c.add(Error, []string{"steps", strconv.Itoa(index[cycle[0]]), "needs"}, "needs cycle: %s", strings.Join(cycle, " → "))
		return
	}

	// Rule 1: the first step, and every other step with no needs and no incoming route, starts
	// the run. The first step may still be a route target (a loop back to the start).
	reached := map[string]bool{}
	for i, st := range c.w.Steps {
		if len(st.Needs) == 0 && (i == 0 || !routedTo[st.ID]) {
			reached[st.ID] = true
		}
	}
	if len(reached) == 0 {
		c.add(Error, []string{"steps", "0", "needs"}, "no entry step: the first step has `needs`, and every other step needs something or is a route target")
		return
	}
	for changed := true; changed; {
		changed = false
		for _, st := range c.w.Steps {
			if reached[st.ID] {
				for _, t := range st.NextHop.On {
					if t != End && !reached[t] {
						reached[t], changed = true, true
					}
				}
				continue
			}
			if len(st.Needs) > 0 && all(st.Needs, func(n string) bool { return reached[n] }) {
				reached[st.ID], changed = true, true
			}
		}
	}
	for i, st := range c.w.Steps {
		if !reached[st.ID] {
			c.add(Error, []string{"steps", strconv.Itoa(i)}, "step %q can never start: nothing routes to it from an entry step", st.ID)
		}
	}
}

// needsCycle returns one cycle in the needs graph, or nil.
func needsCycle(steps []*Step) []string {
	needs := map[string][]string{}
	for _, st := range steps {
		needs[st.ID] = st.Needs
	}
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		state[id] = active
		stack = append(stack, id)
		for _, n := range needs[id] {
			switch state[n] {
			case active:
				for i, s := range stack {
					if s == n {
						return append(append([]string{}, stack[i:]...), n)
					}
				}
			case unseen:
				if c := visit(n); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return nil
	}
	for _, st := range steps {
		if state[st.ID] == unseen {
			if c := visit(st.ID); c != nil {
				return c
			}
		}
	}
	return nil
}

// template parses text as a Go template and checks every field it reads against the context
// templates get (CLAUDE.md section 3): .inputs, .steps, .matrix, .base, .run. step is nil for
// workflow-level templates. secrets says whether `secret` may be called: only in env, since
// everything else can be pasted into a pane, and panes are logged.
func (c *checker) template(path []string, text string, step *Step, secrets bool) {
	if !strings.Contains(text, "{{") {
		return
	}
	t, err := template.New("").Funcs(TemplateFuncs).Option("missingkey=error").Parse(text)
	if err != nil {
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		c.add(Error, path, "bad template: %s", msg)
		return
	}
	if !secrets {
		if _, err := template.New("").Funcs(template.FuncMap{}).Parse(text); err != nil && strings.Contains(err.Error(), `function "secret" not defined`) {
			c.add(Error, path, "`secret` is only allowed in `env`: this text can reach an agent's pane, and panes are logged")
			return
		}
	}
	walkTemplate(t.Root, func(f []string) { c.field(path, f, step) }, true)
}

func (c *checker) field(path []string, f []string, step *Step) {
	ref := "." + strings.Join(f, ".")
	switch f[0] {
	case "base":
	case "run":
		if len(f) > 1 && !contains([]string{"id", "project", "workflow"}, f[1]) {
			c.add(Error, path, "%s: .run has id, project and workflow", ref)
		}
	case "inputs":
		if len(f) > 1 {
			if _, ok := c.w.Inputs[f[1]]; !ok {
				c.add(Error, path, "%s: no input %q is declared under `inputs`", ref, f[1])
			}
		}
	case "matrix":
		if step == nil || len(step.Matrix) == 0 {
			c.add(Error, path, "%s: only a matrix step can use .matrix", ref)
		} else if len(f) > 1 {
			if _, ok := step.Matrix[f[1]]; !ok {
				c.add(Error, path, "%s: the step's matrix has no %q", ref, f[1])
			}
		}
	case "steps":
		if len(f) > 1 && c.w.Step(f[1]) == nil {
			c.add(Error, path, "%s: unknown step %q", ref, f[1])
		} else if len(f) > 2 && f[2] != "out" && f[2] != "exit" {
			c.add(Error, path, "%s: a step has .out.<key> and .exit", ref)
		}
	default:
		c.add(Error, path, "%s: templates see only .inputs, .steps, .matrix, .base and .run", ref)
	}
}

// walkTemplate calls fn with the identifiers of every field chain rooted at the top-level dot:
// ".a.b" while dot is still the root (rootDot), and "$.a.b" anywhere. Inside range and with, dot
// is the current element, so its fields aren't checked there.
func walkTemplate(n parse.Node, fn func([]string), rootDot bool) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n != nil {
			for _, c := range n.Nodes {
				walkTemplate(c, fn, rootDot)
			}
		}
	case *parse.ActionNode:
		walkTemplate(n.Pipe, fn, rootDot)
	case *parse.PipeNode:
		if n != nil {
			for _, cmd := range n.Cmds {
				for _, a := range cmd.Args {
					walkTemplate(a, fn, rootDot)
				}
			}
		}
	case *parse.FieldNode:
		if rootDot {
			fn(n.Ident)
		}
	case *parse.VariableNode:
		if len(n.Ident) > 1 && n.Ident[0] == "$" {
			fn(n.Ident[1:])
		}
	case *parse.ChainNode:
		if _, ok := n.Node.(*parse.DotNode); ok && rootDot {
			fn(n.Field)
		}
		walkTemplate(n.Node, fn, rootDot)
	case *parse.IfNode:
		walkBranch(&n.BranchNode, fn, rootDot, rootDot)
	case *parse.RangeNode:
		walkBranch(&n.BranchNode, fn, rootDot, false)
	case *parse.WithNode:
		walkBranch(&n.BranchNode, fn, rootDot, false)
	case *parse.TemplateNode:
		walkTemplate(n.Pipe, fn, rootDot)
	}
}

// walkBranch walks an if/range/with: the pipe and the else list see the outer dot, the body sees
// bodyDot.
func walkBranch(b *parse.BranchNode, fn func([]string), rootDot, bodyDot bool) {
	walkTemplate(b.Pipe, fn, rootDot)
	walkTemplate(b.List, fn, bodyDot)
	if b.ElseList != nil {
		walkTemplate(b.ElseList, fn, rootDot)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func all(list []string, ok func(string) bool) bool {
	for _, x := range list {
		if !ok(x) {
			return false
		}
	}
	return true
}
