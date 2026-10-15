package spec

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_GH_THIS_REPO", "s3cret")
	data := map[string]any{"inputs": map[string]any{"issue": "42"}, "base": "dev"}
	for _, tt := range []struct{ in, want, err string }{
		{in: "plain", want: "plain"},
		{in: "#{{ .inputs.issue }} into {{ .base }}", want: "#42 into dev"},
		{in: `{{ secret "gh-this-repo" }}`, want: "s3cret"},
		{in: "{{ .inputs.nope }}", err: "nope"},
		{in: `{{ secret "laight-test-missing-xyz" }}`, err: "LAIGHT_SECRET_LAIGHT_TEST_MISSING_XYZ"},
	} {
		got, err := Render("t", tt.in, data)
		switch {
		case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
			t.Errorf("Render(%q) error = %v, want %q", tt.in, err, tt.err)
		case tt.err == "" && (err != nil || got != tt.want):
			t.Errorf("Render(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}
