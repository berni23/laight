package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/berni23/laight/schema"
)

// Severity of a Problem.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Problem is one finding about a spec file, located by its path in the document.
type Problem struct {
	Severity Severity
	File     string
	Line     int      // 0 if unknown
	Path     []string // e.g. steps, 2, next_hop, on, pass
	Msg      string
}

func (p Problem) String() string {
	var b strings.Builder
	b.WriteString(p.File)
	if p.Line > 0 {
		fmt.Fprintf(&b, ":%d", p.Line)
	}
	fmt.Fprintf(&b, ": %s: ", p.Severity)
	if len(p.Path) > 0 {
		b.WriteString(pathString(p.Path) + ": ")
	}
	b.WriteString(p.Msg)
	return b.String()
}

// pathString renders [steps 2 next_hop] as steps[2].next_hop.
func pathString(path []string) string {
	var b strings.Builder
	for i, p := range path {
		if _, err := strconv.Atoi(p); err == nil {
			fmt.Fprintf(&b, "[%s]", p)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}

// HasErrors reports whether any problem is an error.
func HasErrors(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == Error {
			return true
		}
	}
	return false
}

// Load reads one workflow file and checks it. laightDir is the project's .laight folder, against
// which `uses:` and `guidance:` paths resolve. The workflow is nil when there are errors that make
// it unusable (unreadable file, schema errors); otherwise it is returned with defaults applied,
// along with any problems found. Callers decide what to do with warnings.
func Load(laightDir, file string) (*Workflow, []Problem) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, []Problem{{File: file, Msg: err.Error()}}
	}
	doc, root, probs := parseYAML(file, src)
	if probs != nil {
		return nil, probs
	}
	if probs := validateSchema(file, root, doc, "#"); probs != nil {
		return nil, probs
	}
	probs = resolveUses(laightDir, file, root, doc)
	if HasErrors(probs) {
		return nil, probs
	}
	var w Workflow
	if err := remarshal(doc, &w); err != nil {
		return nil, append(probs, Problem{File: file, Msg: err.Error()})
	}
	w.File = file
	applyDefaults(&w)
	probs = append(probs, check(laightDir, &w, root)...)
	sort.SliceStable(probs, func(i, j int) bool {
		if probs[i].File != probs[j].File {
			return probs[i].File < probs[j].File
		}
		return probs[i].Line < probs[j].Line
	})
	return &w, probs
}

// parseYAML decodes YAML into both a node tree (for line numbers) and a plain value (for the schema).
func parseYAML(file string, src []byte) (any, *yaml.Node, []Problem) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, nil, []Problem{{File: file, Msg: "not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}}
	}
	if root.Kind == 0 {
		return nil, nil, []Problem{{File: file, Msg: "the file is empty"}}
	}
	var doc any
	if err := root.Decode(&doc); err != nil {
		return nil, nil, []Problem{{File: file, Msg: err.Error()}}
	}
	doc, err := jsonable(doc)
	if err != nil {
		return nil, nil, []Problem{{File: file, Msg: err.Error()}}
	}
	return doc, &root, nil
}

// jsonable round-trips v through JSON so the schema validator sees JSON types only.
func jsonable(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

func remarshal(from, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

var (
	schemaOnce sync.Once
	schemas    map[string]*jsonschema.Schema // by fragment: "#" or "#/$defs/seat"
	schemaErr  error
)

func compiled(fragment string) (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema.Workflow))
		if err != nil {
			schemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schema.WorkflowURL, doc); err != nil {
			schemaErr = err
			return
		}
		schemas = map[string]*jsonschema.Schema{}
		for _, f := range []string{"#", "#/$defs/seat"} {
			s, err := c.Compile(schema.WorkflowURL + f)
			if err != nil {
				schemaErr = err
				return
			}
			schemas[f] = s
		}
	})
	if schemaErr != nil {
		return nil, schemaErr
	}
	return schemas[fragment], nil
}

var printer = message.NewPrinter(language.English)

// validateSchema checks doc against the workflow schema (or one of its $defs) and turns each
// leaf error into a Problem with a line number.
func validateSchema(file string, root *yaml.Node, doc any, fragment string) []Problem {
	s, err := compiled(fragment)
	if err != nil {
		return []Problem{{File: file, Msg: "internal: bad embedded schema: " + err.Error()}}
	}
	err = s.Validate(doc)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []Problem{{File: file, Msg: err.Error()}}
	}
	var probs []Problem
	seen := map[string]bool{}
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		add := func(at []string, msg string) {
			key := strings.Join(at, "/") + "\x00" + msg
			if seen[key] {
				return
			}
			seen[key] = true
			probs = append(probs, Problem{File: file, Line: lineOf(root, at), Path: at, Msg: msg})
		}
		if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
			for _, prop := range k.Properties {
				add(append(append([]string{}, e.InstanceLocation...), prop), "unknown field")
			}
			return
		}
		add(e.InstanceLocation, e.ErrorKind.LocalizedString(printer))
	}
	walk(ve)
	return probs
}

// lineOf returns the line of the node at path, or of its deepest existing ancestor.
func lineOf(root *yaml.Node, path []string) int {
	_, line := nodeAt(root, path)
	return line
}

// nodeAt returns the node at path (nil if there is none) and the line of it, or of its deepest
// existing ancestor. A mapping value's line is its key's.
func nodeAt(root *yaml.Node, path []string) (*yaml.Node, int) {
	if root == nil {
		return nil, 0
	}
	n := root
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	line := n.Line
	for _, p := range path {
		var next *yaml.Node
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == p {
					line = n.Content[i].Line
					next = n.Content[i+1]
					break
				}
			}
		case yaml.SequenceNode:
			if i, err := strconv.Atoi(p); err == nil && i >= 0 && i < len(n.Content) {
				next = n.Content[i]
				line = next.Line
			}
		}
		if next == nil {
			return nil, line
		}
		n = next
	}
	return n, line
}

// rewrittenEnv warns about each unquoted env value under path that isn't passed on as written:
// YAML resolves 0x1F to 31 and 1.10 to 1.1 before laight sees it (#44). passed gives the text a
// key is passed on as; false skips the key. A value that is an alias is checked as its anchor; an
// env that is an alias, and `<<` merge keys, are not checked.
func rewrittenEnv(file string, root *yaml.Node, path []string, passed func(key string) (string, bool)) []Problem {
	env, _ := nodeAt(root, path)
	if env == nil || env.Kind != yaml.MappingNode {
		return nil
	}
	var probs []Problem
	for i := 0; i+1 < len(env.Content); i += 2 {
		k, v := env.Content[i], env.Content[i+1]
		if v.Kind == yaml.AliasNode {
			v = v.Alias
		}
		if v.Kind != yaml.ScalarNode || v.ShortTag() == "!!str" {
			continue
		}
		if got, ok := passed(k.Value); ok && got != v.Value {
			at := append(append([]string{}, path...), k.Value)
			probs = append(probs, Problem{Severity: Warning, File: file, Line: k.Line, Path: at,
				Msg: fmt.Sprintf("%s is passed on as %q; quote it to pass it as written", v.Value, got)})
		}
	}
	return probs
}

// resolveUses replaces each seat's `uses:` with the shared seat file's fields, overridden by the
// fields written inline.
func resolveUses(laightDir, file string, root *yaml.Node, doc any) []Problem {
	m, _ := doc.(map[string]any)
	seats, _ := m["seats"].(map[string]any)
	var probs []Problem
	// A shared file used by several seats reports its problems once; its env is checked once
	// for the seats that don't override env.
	reported := map[string]bool{}
	report := func(key string, ps []Problem) {
		if !reported[key] {
			reported[key] = true
			probs = append(probs, ps...)
		}
	}
	names := make([]string, 0, len(seats))
	for name := range seats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		seat, _ := seats[name].(map[string]any)
		uses, _ := seat["uses"].(string)
		if uses == "" {
			continue
		}
		at := []string{"seats", name, "uses"}
		path := filepath.Join(laightDir, filepath.FromSlash(uses))
		src, err := os.ReadFile(path)
		if err != nil {
			probs = append(probs, Problem{File: file, Line: lineOf(root, at), Path: at,
				Msg: fmt.Sprintf("can't read %s: %v", uses, unwrapPathErr(err))})
			continue
		}
		base, baseRoot, ps := parseYAML(path, src)
		if ps != nil {
			report(path, ps)
			continue
		}
		if ps := validateSchema(path, baseRoot, base, "#/$defs/seat"); ps != nil {
			report(path, ps)
			continue
		}
		baseMap := base.(map[string]any)
		if _, nested := baseMap["uses"]; nested {
			report(path, []Problem{{File: path, Line: lineOf(baseRoot, []string{"uses"}), Path: []string{"uses"},
				Msg: "a shared seat file can't itself use another one"}})
			continue
		}
		if _, own := seat["env"]; !own {
			baseEnv, _ := baseMap["env"].(map[string]any)
			report(path+"#env", rewrittenEnv(path, baseRoot, []string{"env"}, func(k string) (string, bool) {
				v, ok := baseEnv[k]
				return envString(v), ok
			}))
		}
		merged := map[string]any{}
		for k, v := range baseMap {
			merged[k] = v
		}
		for k, v := range seat {
			if k != "uses" {
				merged[k] = v
			}
		}
		seats[name] = merged
	}
	return probs
}

func unwrapPathErr(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Err
	}
	return err
}

func applyDefaults(w *Workflow) {
	if w.Base == "" {
		w.Base = DefaultBase
	}
	if w.LoopGuards.MaxHops == 0 {
		w.LoopGuards.MaxHops = DefaultMaxHops
	}
	if w.OnFinish.Success == "" {
		w.OnFinish.Success = "destroy"
	}
	if w.OnFinish.Failure == "" {
		w.OnFinish.Failure = "keep"
	}
	for _, s := range w.Seats {
		if s.Runtime == "" {
			s.Runtime = "claude-code"
		}
		if s.Cwd == "" {
			s.Cwd = "worktree"
		}
		if s.Session == "" {
			s.Session = "resume"
		}
		if s.Permissions == "" {
			s.Permissions = "default"
		}
	}
	for _, st := range w.Steps {
		if st.Type == "" {
			switch {
			case st.Seat != "":
				st.Type = TypeAgent
			case st.Run != "":
				st.Type = TypeShell
			case st.Ask != "":
				st.Type = TypeGate
			}
		}
		if len(st.AllowedExits) == 0 {
			switch st.Type {
			case TypeAgent:
				st.AllowedExits = []string{DefaultAgentExit}
			case TypeGate:
				st.AllowedExits = []string{"yes", "no"}
			case TypeShell:
				st.AllowedExits = shellExits(st)
			}
		}
		if len(st.Matrix) > 0 && st.MaxParallel == 0 {
			st.MaxParallel = DefaultMaxParallel
		}
		if st.Timeout == nil && st.Type != TypeGate && w.Timeouts.Step != 0 { // a gate already waits on a human
			d := w.Timeouts.Step
			st.Timeout = &d
		}
	}
}

// shellExits is every name in `exits:`, in a stable order, after ok unless `exits:` maps code 0.
func shellExits(st *Step) []string {
	var exits []string
	seen := map[string]bool{ExitFailed: true}
	if _, ok := st.Exits["0"]; !ok {
		exits, seen[ExitOK] = append(exits, ExitOK), true
	}
	codes := make([]string, 0, len(st.Exits))
	for c := range st.Exits {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { a, _ := strconv.Atoi(codes[i]); b, _ := strconv.Atoi(codes[j]); return a < b })
	for _, c := range codes {
		if e := st.Exits[c]; !seen[e] {
			seen[e] = true
			exits = append(exits, e)
		}
	}
	return exits
}
