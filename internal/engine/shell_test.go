package engine

import (
	"bytes"
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
