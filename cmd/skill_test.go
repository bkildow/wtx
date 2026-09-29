package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/skill"
)

// frontmatter returns the name and description fields of a SKILL.md.
func frontmatter(t *testing.T, doc string) (name, description string) {
	t.Helper()
	if !strings.HasPrefix(doc, "---\n") {
		t.Fatalf("skill does not start with frontmatter: %q", doc[:min(len(doc), 40)])
	}
	block, _, ok := strings.Cut(strings.TrimPrefix(doc, "---\n"), "\n---\n")
	if !ok {
		t.Fatal("skill frontmatter is not closed")
	}
	for _, line := range strings.Split(block, "\n") {
		if v, ok := strings.CutPrefix(line, "name: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(line, "description: "); ok {
			description = v
		}
	}
	return name, description
}

func TestSkillCmd_printsSkill(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	runErr := newSkillCmd().RunE(nil, nil)
	os.Stdout = stdout
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if runErr != nil {
		t.Fatalf("unexpected error: %v", runErr)
	}
	if string(out) != skill.Content {
		t.Error("wtx skill output does not match embedded content")
	}

	name, description := frontmatter(t, string(out))
	if name != "wtx" {
		t.Errorf("name = %q, want %q", name, "wtx")
	}
	if description == "" {
		t.Error("description is empty")
	}
	if !strings.Contains(string(out), "wtx add <branch>") {
		t.Error("skill body is missing command reference")
	}
}

func TestSkillWrapper_frontmatter(t *testing.T) {
	data, err := os.ReadFile("../skills/wtx/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	name, description := frontmatter(t, string(data))
	if name != "wtx" {
		t.Errorf("name = %q, want %q", name, "wtx")
	}
	if description == "" {
		t.Error("description is empty")
	}
	if !strings.Contains(string(data), "wtx skill") {
		t.Error("wrapper does not tell the agent to run wtx skill")
	}
}
