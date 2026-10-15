// Package schema embeds laight's JSON Schemas (draft 2020-12) so the binary validates specs
// without network access, and so editors can fetch the same files from the repo.
package schema

import _ "embed"

// Workflow is workflow.schema.json: one file under .laight/workflows/.
//
//go:embed workflow.schema.json
var Workflow []byte

// Config is config.schema.json: ~/.laight/config.yaml and .laight/laight.yaml.
//
//go:embed config.schema.json
var Config []byte

// WorkflowURL is the $id of the workflow schema, used in generated files' yaml-language-server comment.
const WorkflowURL = "https://raw.githubusercontent.com/berni23/laight/main/schema/workflow.schema.json"
