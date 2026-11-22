// Package schema embeds laight's JSON Schemas (draft 2020-12) so the binary validates specs
// without network access, and so editors can fetch the same files from the repo.
package schema

import (
	_ "embed"
	"regexp"
	"strings"
)

// Workflow is workflow.schema.json: one file under .laight/workflows/.
//
//go:embed workflow.schema.json
var Workflow []byte

// Config is config.schema.json: ~/.laight/config.yaml and .laight/laight.yaml.
//
//go:embed config.schema.json
var Config []byte

// WorkflowURL is the $id of the workflow schema: the copy on main.
const WorkflowURL = "https://raw.githubusercontent.com/berni23/laight/main/schema/workflow.schema.json"

// release is a released version (0.1.0, v0.2.0-rc.1), not a snapshot, a Go pseudo-version
// (v0.0.0-20261122093000-abcdef123456) or a dirty build (+dirty).
var release = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
var pseudo = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

// WorkflowURLFor is where an editor fetches the workflow schema that a laight of this version
// validates with (#108): the file at the release's tag, so the editor and `laight validate`
// agree after main moves on; main for a build that isn't a release.
func WorkflowURLFor(version string) string {
	if !release.MatchString(version) || pseudo.MatchString(version) || strings.Contains(version, "SNAPSHOT") {
		return WorkflowURL
	}
	return strings.Replace(WorkflowURL, "/main/", "/v"+strings.TrimPrefix(version, "v")+"/", 1)
}
