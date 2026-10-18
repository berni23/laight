// Package inbox is the human queue of CLAUDE.md section 6 with its default backend, `files`: one
// file per pending item in ~/.laight/inbox/, moved to inbox/answered/ by the first answer. The
// Queue interface of the spec waits for a second backend; until then these functions are it.
package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Item kinds.
const (
	Permission = "permission"
	Gate       = "gate"
	Failed     = "failed"
)

// The choices of a permission item.
const (
	Allow = "allow"
	Deny  = "deny"
)

// The choices of a failed item.
const (
	Retry = "retry"
	Abort = "abort"
)

// Item is one thing waiting for a human.
type Item struct {
	ID       string     `json:"id"`
	Run      string     `json:"run"`
	Repo     string     `json:"repo"`
	Workflow string     `json:"workflow"`
	Step     string     `json:"step,omitempty"`
	Seat     string     `json:"seat,omitempty"`
	Hop      int        `json:"hop,omitempty"`
	Kind     string     `json:"kind"`
	Title    string     `json:"title"`
	Detail   string     `json:"detail,omitempty"` // redacted
	Choices  []string   `json:"choices"`
	Created  time.Time  `json:"created"`
	Expires  *time.Time `json:"expires,omitempty"`
	Answer   string     `json:"answer,omitempty"` // set once answered
	Answered *time.Time `json:"answered,omitempty"`
	// Match identifies what a permission item asks about (a hash of the tool and its input), so
	// the seat's PostToolUse hook can withdraw it when the prompt was answered in the pane.
	Match string `json:"match,omitempty"`
}

// Short is the id people type: the item's last six characters, lower case.
func (it *Item) Short() string { return strings.ToLower(it.ID[max(0, len(it.ID)-6):]) }

// ErrAlreadyAnswered is returned by Answer for an item someone answered first.
var ErrAlreadyAnswered = errors.New("already answered")

func dir(home string) string { return filepath.Join(home, "inbox") }

func pendingFile(home, id string) string  { return filepath.Join(dir(home), id+".json") }
func answeredFile(home, id string) string { return filepath.Join(dir(home), "answered", id+".json") }

// Put queues an item. Its title and detail are redacted and cleaned here, so no caller can
// forget: the title leaves the machine (§6), and both reach a terminal. Inside a seat, that
// includes the seat's secret env values. Putting an item that is already queued or answered
// does nothing.
func Put(home string, it Item) error {
	if exists(pendingFile(home, it.ID)) || exists(answeredFile(home, it.ID)) {
		return nil
	}
	if it.Created.IsZero() {
		it.Created = time.Now().UTC()
	}
	for _, v := range secretForms(SeatSecrets()) {
		it.Title, it.Detail = strings.ReplaceAll(it.Title, v, "***"), strings.ReplaceAll(it.Detail, v, "***")
	}
	it.Title = clean(Redact(it.Title), false)
	it.Detail = cut(clean(Redact(it.Detail), true)) // cut last: a cut secret no longer matches
	return writeFile(pendingFile(home, it.ID), &it, os.Rename)
}

// Pending returns the items waiting for an answer, oldest first. An item that can't be read is
// still waiting on someone: it comes back in the error, next to the items that could be read.
func Pending(home string) ([]*Item, error) {
	files, err := filepath.Glob(filepath.Join(dir(home), "*.json"))
	if err != nil {
		return nil, err
	}
	var items []*Item
	var errs []error
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if exists(answeredFile(home, id)) {
			continue // answered, and the answerer died before removing it
		}
		it, err := read(f)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, errors.Join(errs...)
}

// Get returns a pending item.
func Get(home, id string) (*Item, error) { return read(pendingFile(home, id)) }

// Answered returns the answered item, or nil while it is pending.
func Answered(home, id string) (*Item, error) {
	it, err := read(answeredFile(home, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return it, err
}

// Answer records choice as the item's answer. The first answer wins: a hard link fails if the
// answered file exists, where a rename would replace it. Later answers get ErrAlreadyAnswered.
func Answer(home, id, choice string) (*Item, error) {
	it, err := read(pendingFile(home, id))
	if errors.Is(err, os.ErrNotExist) {
		if exists(answeredFile(home, id)) {
			return lost(home, id)
		}
		return nil, fmt.Errorf("no item %s", id)
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains(it.Choices, choice) {
		return nil, fmt.Errorf("%q is not an answer to %s; choose one of: %s", choice, it.Short(), strings.Join(it.Choices, ", "))
	}
	now := time.Now().UTC()
	it.Answer, it.Answered = choice, &now
	err = writeFile(answeredFile(home, id), it, os.Link)
	if errors.Is(err, os.ErrExist) {
		return lost(home, id)
	}
	if err != nil {
		return nil, err
	}
	os.Remove(pendingFile(home, id))
	return it, nil
}

// lost is Answer's reply to a late answer: the winning one, and ErrAlreadyAnswered. An answer
// that can't be read is a plain error, so ErrAlreadyAnswered always comes with an item.
func lost(home, id string) (*Item, error) {
	done, err := Answered(home, id)
	if err != nil {
		return nil, err
	}
	if done == nil {
		return nil, fmt.Errorf("item %s: its answer disappeared", id)
	}
	return done, ErrAlreadyAnswered
}

// Find resolves what someone typed to one item: a prefix of its short or its full id, among the
// pending items and then the answered ones (so a late answer hears that it was late).
func Find(home, ref string) (string, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" {
		return "", errors.New("no inbox item given")
	}
	var match []string
	for _, pattern := range []string{"*.json", filepath.Join("answered", "*.json")} {
		files, _ := filepath.Glob(filepath.Join(dir(home), pattern))
		for _, f := range files {
			id := strings.TrimSuffix(filepath.Base(f), ".json")
			short := strings.ToLower(id[max(0, len(id)-6):])
			if strings.HasPrefix(short, ref) || strings.HasPrefix(strings.ToLower(id), ref) {
				if !slices.Contains(match, id) {
					match = append(match, id)
				}
			}
		}
		if len(match) > 0 {
			break
		}
	}
	switch len(match) {
	case 0:
		return "", fmt.Errorf("no inbox item %q", ref)
	case 1:
		return match[0], nil
	}
	return "", fmt.Errorf("%q matches %d items; type more of the id", ref, len(match))
}

// Drop removes a pending item nobody waits on any more (its hook was cancelled). An answer that
// won the race is kept.
func Drop(home, id string) { os.Remove(pendingFile(home, id)) }

// Withdraw drops the run's pending items: the run ended, so nobody is waiting on them.
func Withdraw(home, run string) {
	items, _ := Pending(home)
	for _, it := range items {
		if it.Run == run {
			os.Remove(pendingFile(home, it.ID))
		}
	}
}

// RedactEnv is the seat env variable that names the keys holding a secret (never the values).
const RedactEnv = "LAIGHT_REDACT_ENV"

// SeatSecrets returns the secret values in this process's env: a seat's, for a hook running in
// it; none in the engine's.
func SeatSecrets() []string {
	var vals []string
	for _, k := range strings.Split(os.Getenv(RedactEnv), ",") {
		if v := os.Getenv(k); k != "" && v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

// secretForms is each secret as it may appear in a detail: as is, and inside a JSON string (a
// tool's input), with and without HTML escaping. Longest first, so a secret that contains
// another is stripped whole.
func secretForms(secrets []string) []string {
	var forms []string
	for _, v := range secrets {
		forms = append(forms, v)
		html, _ := json.Marshal(v)
		var plain bytes.Buffer
		enc := json.NewEncoder(&plain)
		enc.SetEscapeHTML(false)
		enc.Encode(v)
		for _, b := range [][]byte{html, bytes.TrimSpace(plain.Bytes())} {
			if f := string(b[1 : len(b)-1]); !slices.Contains(forms, f) {
				forms = append(forms, f)
			}
		}
	}
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	return forms
}

// secretPatterns are what a token looks like (OpenRig's list, CLAUDE.md §12.4).
var secretPatterns = regexp.MustCompile(strings.Join([]string{
	`sk-[A-Za-z0-9_-]{16,}`,
	`gh[pousr]_[A-Za-z0-9]{20,}`,
	`github_pat_[A-Za-z0-9_]{20,}`,
	`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`,
}, "|"))

// longToken is a long run that may be a token; not `/`, so paths survive.
var longToken = regexp.MustCompile(`[A-Za-z0-9_=-]{40,}`)

// Redact replaces anything that looks like a token with ***. A long run counts only when it mixes
// upper case, lower case and digits, as random keys do: a commit SHA or a branch name doesn't.
func Redact(s string) string {
	s = secretPatterns.ReplaceAllString(s, "***")
	return longToken.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") && strings.ContainsAny(m, "0123456789") {
			return "***"
		}
		return m
	})
}

// clean replaces control characters (an escape sequence an agent printed, say) with spaces;
// newlines and tabs are kept when lines is set.
func clean(s string, lines bool) string {
	return strings.Map(func(r rune) rune {
		if lines && (r == '\n' || r == '\t') {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

// cut keeps a detail to an excerpt (§6).
func cut(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + "…"
}

func exists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}

func read(file string) (*Item, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var it Item
	if err := json.Unmarshal(b, &it); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &it, nil
}

// writeFile writes it to a temp file and moves it into place with place (os.Rename, or os.Link
// to fail when the file exists).
func writeFile(file string, it *Item, place func(string, string) error) error {
	b, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".item-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(bytes.TrimSpace(b), '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return place(tmp.Name(), file)
}
