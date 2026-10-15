// Package openrig converts an OpenRig setup (a .openrig/ folder: rig.yaml, workflows/*.yaml and
// agents/*) into laight workflow files. Only the shapes are read; no OpenRig code is used.
package openrig

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/berni23/laight/schema"
)

type rigFile struct {
	Name             string `yaml:"name"`
	PermissionPolicy string `yaml:"permission_policy"`
	Pods             []struct {
		ID      string `yaml:"id"`
		Members []struct {
			ID            string `yaml:"id"`
			Label         string `yaml:"label"`
			AgentRef      string `yaml:"agent_ref"`
			Runtime       string `yaml:"runtime"`
			Cwd           string `yaml:"cwd"`
			RestorePolicy string `yaml:"restore_policy"`
		} `yaml:"members"`
	} `yaml:"pods"`
}

type workflowFile struct {
	Workflow struct {
		ID        string `yaml:"id"`
		Objective string `yaml:"objective"`
		Roles     map[string]struct {
			PreferredTargets []string `yaml:"preferred_targets"`
		} `yaml:"roles"`
		Steps []struct {
			ID           string   `yaml:"id"`
			ActorRole    string   `yaml:"actor_role"`
			Objective    string   `yaml:"objective"`
			AllowedExits []string `yaml:"allowed_exits"`
			NextHop      struct {
				Mode string            `yaml:"mode"`
				On   map[string]string `yaml:"on"`
			} `yaml:"next_hop"`
		} `yaml:"steps"`
		LoopGuards struct {
			MaxHops int `yaml:"max_hops"`
		} `yaml:"loop_guards"`
	} `yaml:"workflow"`
}

type agentFile struct {
	Resources struct {
		Guidance []struct {
			Path string `yaml:"path"`
		} `yaml:"guidance"`
	} `yaml:"resources"`
}

// Result is what an import produces: files relative to .laight/, and notes for the user about
// what changed in meaning.
type Result struct {
	Files map[string][]byte
	Notes []string
}

type seat struct {
	name, label, runtime, session, guidance, origCwd string
}

// Import reads the OpenRig folder dir. base is the branch the workflows work against.
func Import(dir, base string) (*Result, error) {
	var rig rigFile
	if err := readYAML(filepath.Join(dir, "rig.yaml"), &rig); err != nil {
		return nil, err
	}
	res := &Result{Files: map[string][]byte{}}
	note := func(format string, args ...any) { res.Notes = append(res.Notes, fmt.Sprintf(format, args...)) }

	// Seats, by OpenRig session name (<pod>-<member>@<rig>).
	seats := map[string]*seat{}
	taken := map[string]bool{}
	for _, pod := range rig.Pods {
		for _, m := range pod.Members {
			name := m.ID
			if taken[name] {
				name = pod.ID + "-" + m.ID
			}
			taken[name] = true
			s := &seat{name: name, label: m.Label, runtime: m.Runtime, session: "resume", origCwd: m.Cwd}
			if s.runtime == "" {
				s.runtime = "claude-code"
			}
			if m.RestorePolicy == "relaunch_fresh" {
				s.session = "fresh"
			}
			g, err := guidance(dir, m.AgentRef)
			if err != nil {
				return nil, fmt.Errorf("seat %s: %w", name, err)
			}
			if g != nil {
				s.guidance = "guidance/" + name + ".md"
				res.Files[s.guidance] = g
				if n := len(rigCommand.FindAll(g, -1)); n > 0 {
					note("guidance/%s.md still tells the agent to run `rig …` (%d places): replace that with `laight done <exit> key=value`", name, n)
				}
			}
			seats[pod.ID+"-"+m.ID+"@"+rig.Name] = s
		}
	}
	if len(seats) == 0 {
		return nil, fmt.Errorf("%s: no pods with members", filepath.Join(dir, "rig.yaml"))
	}
	perm := "auto"
	if strings.Contains(rig.PermissionPolicy, "yolo") {
		note("permission_policy %s became `permissions: auto`: prompts go to `laight inbox`. Set `permissions: yolo` to keep the old behaviour", rig.PermissionPolicy)
	}
	for _, s := range sortedSeats(seats) {
		note("seat %s: cwd %q became `cwd: worktree`, a fresh worktree per run", s.name, s.origCwd)
	}

	files, err := filepath.Glob(filepath.Join(dir, "workflows", "*.y*ml"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no workflow files", filepath.Join(dir, "workflows"))
	}
	for _, f := range files {
		var wf workflowFile
		if err := readYAML(f, &wf); err != nil {
			return nil, err
		}
		w := wf.Workflow
		id := strings.TrimPrefix(w.ID, rig.Name+"-")
		if id == "" {
			return nil, fmt.Errorf("%s: no workflow.id", f)
		}

		used := map[string]*seat{}
		steps := seq()
		for _, st := range w.Steps {
			role, ok := w.Roles[st.ActorRole]
			if !ok || len(role.PreferredTargets) == 0 {
				return nil, fmt.Errorf("%s: step %s: role %q has no preferred_targets", f, st.ID, st.ActorRole)
			}
			s, ok := seats[role.PreferredTargets[0]]
			if !ok {
				return nil, fmt.Errorf("%s: step %s: %s is not a seat of rig %s", f, st.ID, role.PreferredTargets[0], rig.Name)
			}
			used[s.name] = s
			step := mapping("id", st.ID, "seat", s.name)
			add(step, "objective", literal(oneLine(st.Objective)))
			var exits []string
			for _, e := range st.AllowedExits {
				if e != "failed" {
					exits = append(exits, e)
				}
			}
			if !(len(exits) == 1 && exits[0] == "handoff") && len(exits) > 0 {
				add(step, "allowed_exits", flow(seq(strs(exits...)...)))
			}
			if _, ok := st.NextHop.On["failed"]; ok {
				note("%s, step %s routes `failed`, which in laight also catches crashes and timeouts: consider a named exit (e.g. `fail`) in allowed_exits and the route", id, st.ID)
			}
			if st.NextHop.Mode != "forbid" && len(st.NextHop.On) > 0 {
				on := mapping()
				for _, k := range sortedKeys(st.NextHop.On) {
					add(on, k, st.NextHop.On[k])
				}
				add(step, "next_hop", flow(mapping("on", flow(on))))
			}
			steps.Content = append(steps.Content, step)
		}

		seatsNode := mapping()
		for _, s := range sortedSeats(used) {
			n := mapping()
			if s.label != "" {
				add(n, "label", s.label)
			}
			add(n, "runtime", s.runtime, "cwd", "worktree", "session", s.session, "permissions", perm)
			if s.guidance != "" {
				add(n, "guidance", s.guidance)
			}
			add(seatsNode, s.name, n)
		}

		doc := mapping("version", 1, "id", id)
		if w.Objective != "" {
			add(doc, "summary", oneLine(w.Objective))
		}
		add(doc, "base", base, "seats", seatsNode, "steps", steps)
		if w.LoopGuards.MaxHops > 0 {
			add(doc, "loop_guards", flow(mapping("max_hops", w.LoopGuards.MaxHops)))
		}
		doc.HeadComment = "yaml-language-server: $schema=" + schema.WorkflowURL + "\nImported from OpenRig: " + filepath.Join(filepath.Base(dir), "workflows", filepath.Base(f))

		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
		// YAML 1.1 read `on` as a boolean, so the encoder quotes it; laight reads YAML 1.2.
		res.Files["workflows/"+id+".yaml"] = bytes.ReplaceAll(buf.Bytes(), []byte(`{"on": `), []byte(`{on: `))
	}
	return res, nil
}

var rigCommand = regexp.MustCompile(`\brig (workflow|queue|ps|up|down|send)\b`)

// guidance concatenates an agent's guidance files ("local:agents/x" → agents/x/agent.yaml).
func guidance(dir, ref string) ([]byte, error) {
	if !strings.HasPrefix(ref, "local:") {
		return nil, nil
	}
	agentDir := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(ref, "local:")))
	var a agentFile
	if err := readYAML(filepath.Join(agentDir, "agent.yaml"), &a); err != nil {
		return nil, err
	}
	var out []byte
	for _, g := range a.Resources.Guidance {
		b, err := os.ReadFile(filepath.Join(agentDir, filepath.FromSlash(g.Path)))
		if err != nil {
			return nil, err
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, b...)
	}
	return out, nil
}

func readYAML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func sortedSeats(m map[string]*seat) []*seat {
	out := make([]*seat, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// YAML node helpers: building nodes keeps key order and lets the encoder do the quoting.

func node(v any) *yaml.Node {
	if n, ok := v.(*yaml.Node); ok {
		return n
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		panic(err)
	}
	return &n
}

func mapping(kv ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	add(n, kv...)
	return n
}

func add(m *yaml.Node, kv ...any) {
	for i := 0; i+1 < len(kv); i += 2 {
		m.Content = append(m.Content, node(kv[i]), node(kv[i+1]))
	}
}

func seq(items ...*yaml.Node) *yaml.Node { return &yaml.Node{Kind: yaml.SequenceNode, Content: items} }

func strs(ss ...string) []*yaml.Node {
	out := make([]*yaml.Node, len(ss))
	for i, s := range ss {
		out[i] = node(s)
	}
	return out
}

func flow(n *yaml.Node) *yaml.Node { n.Style = yaml.FlowStyle; return n }

// literal wraps long text in a block scalar so objectives stay readable.
func literal(s string) *yaml.Node {
	n := node(s)
	if len(s) > 60 {
		n.Value = wrap(s, 96) + "\n"
		n.Style = yaml.LiteralStyle
	}
	return n
}

func wrap(s string, width int) string {
	var b strings.Builder
	col := 0
	for i, w := range strings.Fields(s) {
		if i > 0 {
			if col+1+len(w) > width {
				b.WriteByte('\n')
				col = 0
			} else {
				b.WriteByte(' ')
				col++
			}
		}
		b.WriteString(w)
		col += len(w)
	}
	return b.String()
}
