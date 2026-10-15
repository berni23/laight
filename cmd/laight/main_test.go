package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{args: []string{"version"}, code: 0, inStdout: "laight dev"},
		{args: []string{"help"}, code: 0, inStdout: "Usage:"},
		{args: nil, code: 2, inStderr: "Usage:"},
		{args: []string{"nope"}, code: 2, inStderr: `unknown command "nope"`},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != tt.code {
			t.Errorf("run(%q) = %d, want %d", tt.args, code, tt.code)
		}
		if !strings.Contains(out.String(), tt.inStdout) {
			t.Errorf("run(%q) stdout = %q, want it to contain %q", tt.args, out.String(), tt.inStdout)
		}
		if !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, errOut.String(), tt.inStderr)
		}
	}
}
