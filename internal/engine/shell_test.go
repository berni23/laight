package engine

import (
	"bytes"
	"strings"
	"testing"

	"github.com/berni23/laight/internal/spec"
)

func TestRedactor(t *testing.T) {
	var b bytes.Buffer
	r := &redactor{w: &b, secrets: []string{"s3cret", "tok"}}
	for _, p := range []string{"a s3c", "ret b\nline tok", "en\nno newline s3", "cret"} {
		r.Write([]byte(p))
	}
	r.Flush()
	if want := "a *** b\nline ***en\nno newline ***"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

// A secret that spans a line break, or the cut the redactor makes in a long line, is still caught.
func TestRedactorSpans(t *testing.T) {
	var b bytes.Buffer
	r := &redactor{w: &b, secrets: []string{"line1\nline2"}}
	r.Write([]byte("a line1\n"))
	r.Write([]byte("line2 b\n"))
	r.Flush()
	if want := "a *** b\n"; b.String() != want {
		t.Errorf("multi-line: got %q, want %q", b.String(), want)
	}

	b.Reset()
	r = &redactor{w: &b, secrets: []string{"s3cret"}}
	long := strings.Repeat("x", 70000) + "s3cret" + strings.Repeat("y", 32765) // no newline
	r.Write([]byte(long))
	r.Flush()
	if got := b.String(); strings.Contains(got, "s3c") || got != strings.Replace(long, "s3cret", "***", 1) {
		t.Errorf("long line: secret not redacted (%d bytes out)", len(got))
	}
}

func TestShellExit(t *testing.T) {
	st := &spec.Step{Exits: map[string]string{"3": "retry", "0": "clean"}}
	for code, want := range map[int]string{0: "clean", 3: "retry", 1: "failed", -1: "failed"} {
		if got := shellExit(st, code); got != want {
			t.Errorf("shellExit(%d) = %s, want %s", code, got, want)
		}
	}
	if got := shellExit(&spec.Step{}, 0); got != "ok" {
		t.Errorf("shellExit(0) without exits = %s", got)
	}
}
