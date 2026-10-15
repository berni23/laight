package spec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirName is the folder in a repo that holds laight's specs.
const DirName = ".laight"

// ErrNoProject means no .laight folder was found from the current directory up.
var ErrNoProject = errors.New("no .laight/ folder here or above; run `laight init` first")

// Project is a repo with a .laight folder.
type Project struct {
	Root string // the folder that contains .laight
	Dir  string // Root/.laight
}

// FindProject walks up from start to the nearest folder that contains .laight/.
func FindProject(start string) (*Project, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, DirName)); err == nil && fi.IsDir() {
			return &Project{Root: dir, Dir: filepath.Join(dir, DirName)}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, ErrNoProject
		}
		dir = parent
	}
}

// Workflows returns the project's workflow files, sorted.
func (p *Project) Workflows() ([]string, error) {
	var files []string
	for _, pat := range []string{"*.yaml", "*.yml"} {
		m, err := filepath.Glob(filepath.Join(p.Dir, "workflows", pat))
		if err != nil {
			return nil, err
		}
		files = append(files, m...)
	}
	sort.Strings(files)
	return files, nil
}

// WorkflowFile finds a workflow by id (the file name without extension) or by path.
func (p *Project) WorkflowFile(name string) (string, error) {
	if strings.ContainsAny(name, `/\`) || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		if _, err := os.Stat(name); err != nil {
			return "", err
		}
		return name, nil
	}
	files, err := p.Workflows()
	if err != nil {
		return "", err
	}
	var ids []string
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if id == name {
			return f, nil
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("no workflow %q: .laight/workflows/ is empty", name)
	}
	return "", fmt.Errorf("no workflow %q (workflows: %s)", name, strings.Join(ids, ", "))
}
