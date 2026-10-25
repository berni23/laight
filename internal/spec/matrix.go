package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// MaxCopies bounds a matrix: each copy may make a worktree, so a runaway list from an earlier
// step fails the step instead of starting dozens of agents.
const MaxCopies = 32

// Instance is the seat instance a matrix copy runs on: dev#2 for copy 2 of a step on seat dev.
func Instance(seat string, copy int) string { return fmt.Sprintf("%s#%d", seat, copy) }

// SeatOf returns the seat a seat or seat instance (dev#2) belongs to, or nil.
func (w *Workflow) SeatOf(name string) *Seat {
	base, _, _ := strings.Cut(name, "#")
	return w.Seats[base]
}

// MatrixOnly reports whether every step on seat is a matrix step: the seat itself never starts,
// only its instances do, each with its copy.
func (w *Workflow) MatrixOnly(seat string) bool {
	used := false
	for _, st := range w.Steps {
		if st.Seat == seat {
			if len(st.Matrix) == 0 {
				return false
			}
			used = true
		}
	}
	return used
}

// Copies expands a matrix step into its copies, in a fixed order: keys sorted, then the
// cartesian product in list order (the first key varies slowest). A value is a list, or a
// template: one that is a single action ({{ .steps.plan.out.tasks }}) gives its value, anything
// else must render a JSON list. Every element must be a scalar.
func Copies(st *Step, data map[string]any) ([]map[string]any, error) {
	keys := make([]string, 0, len(st.Matrix))
	for k := range st.Matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	copies := []map[string]any{{}}
	for _, k := range keys {
		list, err := matrixList(st.ID+".matrix."+k, st.Matrix[k], data)
		if err != nil {
			return nil, fmt.Errorf("matrix %s: %w", k, err)
		}
		if len(copies)*len(list) > MaxCopies {
			return nil, fmt.Errorf("matrix makes more than %d copies", MaxCopies)
		}
		var next []map[string]any
		for _, c := range copies {
			for _, v := range list {
				m := map[string]any{k: v}
				for ck, cv := range c {
					m[ck] = cv
				}
				next = append(next, m)
			}
		}
		copies = next
	}
	return copies, nil
}

func matrixList(name string, v any, data map[string]any) ([]any, error) {
	if s, ok := v.(string); ok {
		var err error
		if v, err = evalTemplate(name, s, data); err != nil {
			return nil, err
		}
	}
	if s, ok := v.(string); ok {
		var err error
		if v, err = DecodeJSON(s); err != nil {
			return nil, fmt.Errorf("%q is not a JSON list", s)
		}
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%v is not a list", v)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("the list is empty")
	}
	for _, x := range list {
		switch x.(type) {
		case string, bool, json.Number, int, float64:
		default:
			return nil, fmt.Errorf("element %v is not a string, number or boolean", x)
		}
	}
	return list, nil
}

// DecodeJSON decodes s as exactly one JSON value, numbers kept as json.Number. Anything after the
// value, even a stray bracket, is an error rather than silently dropped.
func DecodeJSON(s string) (any, error) {
	var v any
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

// evalTemplate is Render, except that a template made of one action returns the action's value
// rather than its printed form, so a list output stays a list.
func evalTemplate(name, text string, data map[string]any) (any, error) {
	text = strings.TrimSpace(text)
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, err
	}
	nodes := t.Tree.Root.Nodes
	if len(nodes) != 1 || nodes[0].Type() != parse.NodeAction {
		return Render(name, text, data)
	}
	var value any
	keep := template.FuncMap{"laightValue": func(v any) string { value = v; return "" }}
	pipe := nodes[0].(*parse.ActionNode).Pipe.String()
	t, err = template.New(name).Option("missingkey=error").Funcs(keep).Parse("{{ laightValue (" + pipe + ") }}")
	if err != nil {
		return nil, err
	}
	if err := t.Execute(&bytes.Buffer{}, data); err != nil {
		return nil, err
	}
	return value, nil
}
