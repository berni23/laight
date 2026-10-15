// Package scaffold writes a starter .laight/ folder for `laight init`.
package scaffold

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed files
var files embed.FS

// Files returns the starter files, relative to .laight/, with base filled in.
func Files(base string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(path, "files/")] = []byte(strings.ReplaceAll(string(b), "{{BASE}}", base))
		return nil
	})
	return out, err
}
