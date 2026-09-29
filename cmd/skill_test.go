package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseFrontmatter decodes the YAML frontmatter of a SKILL.md and checks the
// fields every skill needs.
func parseFrontmatter(t *testing.T, doc string) skillFrontmatter {
	t.Helper()
	block, _, ok := strings.Cut(strings.TrimPrefix(doc, "---\n"), "\n---\n")
	if !strings.HasPrefix(doc, "---\n") || !ok {
		t.Fatal("skill does not start with a closed frontmatter block")
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(block), &fm); err != nil {
		t.Fatalf("invalid frontmatter: %v", err)
	}
	if fm.Name != "wtx" {
		t.Errorf("name = %q, want %q", fm.Name, "wtx")
	}
	if fm.Description == "" {
		t.Error("description is empty")
	}
	return fm
}

func TestSkillCmd_printsSkill(t *testing.T) {
	var out bytes.Buffer
	c := newSkillCmd()
	c.SetOut(&out)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.String() != skillContent {
		t.Error("wtx skill output does not match embedded content")
	}
	parseFrontmatter(t, out.String())
	if !strings.Contains(out.String(), "wtx add <branch>") {
		t.Error("skill body is missing command reference")
	}
}

// The installable wrapper must advertise the same skill as `wtx skill`.
func TestSkillWrapper_matchesEmbeddedSkill(t *testing.T) {
	data, err := os.ReadFile("../skills/wtx/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := parseFrontmatter(t, string(data))
	if embedded := parseFrontmatter(t, skillContent); wrapper != embedded {
		t.Errorf("wrapper frontmatter %+v differs from embedded %+v", wrapper, embedded)
	}
	if !strings.Contains(string(data), "wtx skill") {
		t.Error("wrapper does not tell the agent to run wtx skill")
	}
}
