package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// rawLoad matches the calls that rebuild what a Clone already resolves: the
// config, the git Runner and the Project root. A Clone threads --dry-run into
// its Runner and resolves the per-clone ~/.wtx paths; these calls skip both.
// findProjectRoot is cmd's own wrapper around project.FindRoot.
var rawLoad = regexp.MustCompile(`\b(config\.Load|config\.Exists|git\.NewRunner|project\.FindRoot|findProjectRoot)\(`)

// rawLoadAllowed marks a line that genuinely runs before any Clone exists.
const rawLoadAllowed = "// raw-load: "

// TestCommandsReadThroughTheClone keeps commands on openClone and
// project.OpenAt instead of loading config or building a Runner by hand.
func TestCommandsReadThroughTheClone(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			call := rawLoad.FindString(line)
			if call == "" || strings.Contains(line, rawLoadAllowed) || strings.HasPrefix(line, "func ") {
				continue
			}
			t.Errorf("cmd/%s:%d calls %s; open the Clone with openClone(ctx), or project.OpenAt(ctx, root, ...) for a known root, and use its Config(), Runner() and Root(). "+
				"Only code that runs before a Clone can exist (init, clone) may end the line with `%s<reason>`",
				file, i+1, strings.TrimSuffix(call, "("), rawLoadAllowed)
		}
	}
}
