// Package scaffold writes a starter .laight/ folder for `laight init`.
package scaffold

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed files
var files embed.FS

// Files returns the starter files, relative to .laight/, with base and the workflow schema's URL
// filled in.
func Files(base, schemaURL string) (map[string][]byte, error) {
	fill := strings.NewReplacer("{{BASE}}", base, "{{SCHEMA}}", schemaURL)
	out := map[string][]byte{}
	err := fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(path, "files/")] = []byte(fill.Replace(string(b)))
		return nil
	})
	return out, err
}
