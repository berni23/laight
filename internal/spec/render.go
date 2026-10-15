package spec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"text/template"
	"time"
)

// Render executes a workflow template against data (.inputs, .steps, .matrix, .base, .run).
// `secret "x"` reads $LAIGHT_SECRET_X, then the OS keychain (service "laight", account "x").
// A missing key is an error, never an empty string.
func Render(name, text string, data map[string]any) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{"secret": Secret}).Parse(text)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// Secret returns the named secret. The error never contains the value.
func Secret(name string) (string, error) {
	env := "LAIGHT_SECRET_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	if v, ok := os.LookupEnv(env); ok {
		return v, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "security", "find-generic-password", "-s", "laight", "-a", name, "-w")
	case "linux":
		cmd = exec.CommandContext(ctx, "secret-tool", "lookup", "service", "laight", "account", name)
	}
	if cmd != nil {
		if out, err := cmd.Output(); err == nil {
			return strings.TrimRight(string(out), "\r\n"), nil
		}
	}
	return "", fmt.Errorf("secret %q not found: set %s or add it to the OS keychain (service laight, account %s)", name, env, name)
}
