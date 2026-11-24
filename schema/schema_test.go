package schema

import "testing"

func TestWorkflowURLFor(t *testing.T) {
	const tagged = "https://raw.githubusercontent.com/berni23/laight/v0.1.0/schema/workflow.schema.json"
	for version, want := range map[string]string{
		"0.1.0":                                tagged, // GoReleaser's {{ .Version }}
		"v0.1.0":                               tagged, // go install …@v0.1.0
		"0.2.0-rc.1":                           "https://raw.githubusercontent.com/berni23/laight/v0.2.0-rc.1/schema/workflow.schema.json",
		"dev":                                  WorkflowURL,
		"":                                     WorkflowURL,
		"0.1.1-SNAPSHOT-abc1234":               WorkflowURL, // make snapshot: no such tag
		"v0.0.0-20261122093000-abcdef123456":   WorkflowURL, // go install …@<commit>
		"v0.1.1-0.20261122093000-abcdef123456": WorkflowURL,
		"v0.1.0+dirty":                         WorkflowURL,
		"(devel)":                              WorkflowURL,
	} {
		if got := WorkflowURLFor(version); got != want {
			t.Errorf("%q: %s, want %s", version, got, want)
		}
	}
}
