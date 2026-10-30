package config

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// schemaPath is the committed schema that generated configs reference by URL.
var schemaPath = filepath.Join("..", "..", "schema", "worktree.schema.json")

func TestSchemaURL(t *testing.T) {
	tests := []struct {
		version, ref string
	}{
		{"dev", "main"},
		{"", "main"},
		{"v1.2.3", "v1.2.3"},
		{"1.2.3", "v1.2.3"},
		{"v0.12.0-3-gabc123", "main"},
		{"v0.12.0-3-gabc123-dirty", "main"},
		{"0.12.1-SNAPSHOT-abc123", "main"},
	}
	for _, tt := range tests {
		want := "https://raw.githubusercontent.com/bkildow/wtx/" + tt.ref + "/schema/worktree.schema.json"
		if got := schemaURL(tt.version); got != want {
			t.Errorf("schemaURL(%q) = %q, want %q", tt.version, got, want)
		}
	}
}

func TestRenderAnnotatedStartsWithSchemaModeline(t *testing.T) {
	cfg := DefaultConfig()
	for _, c := range []*Config{nil, &cfg} {
		first, _, _ := strings.Cut(renderAnnotatedConfig(c, "v1.2.3"), "\n")
		if want := "# yaml-language-server: $schema=" + schemaURL("v1.2.3"); first != want {
			t.Errorf("first line = %q, want %q", first, want)
		}
	}
}

// TestSchemaMatchesConfig keeps schema/worktree.schema.json in sync with the
// yaml tags on Config.
func TestSchemaMatchesConfig(t *testing.T) {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Description string          `json:"description"`
			Type        json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse %s: %v", schemaPath, err)
	}

	tags := map[string]bool{}
	typ := reflect.TypeFor[Config]()
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			tags[name] = true
		}
	}

	for _, name := range slices.Sorted(maps.Keys(tags)) {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("Config yaml tag %q has no schema property", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		prop := schema.Properties[name]
		if !tags[name] {
			t.Errorf("schema property %q has no Config yaml tag", name)
		}
		if prop.Description == "" {
			t.Errorf("schema property %q has no description", name)
		}
		if len(prop.Type) == 0 {
			t.Errorf("schema property %q has no type", name)
		}
	}
}

// TestGeneratedConfigsValidateAgainstSchema renders the configs wtx writes
// (config init, clone, init) and validates them against the committed schema.
func TestGeneratedConfigsValidateAgainstSchema(t *testing.T) {
	schema := compileSchema(t)

	bare := DefaultConfig()
	bare.Scripts = map[string]string{"refresh": "bin/refresh"}

	checkout := DefaultConfig()
	checkout.GitDir = ".git"
	checkout.SetWorktreeDir("")
	checkout.SetSharedDir("")

	disabled := false
	full := DefaultConfig()
	full.MainBranch = "develop"
	full.Editor = "cursor"
	full.Setup = []string{"npm install"}
	full.ParallelSetup = []string{"bundle install"}
	full.Teardown = []string{"docker compose down"}
	full.ParallelTeardown = []string{"make clean"}
	full.BackgroundSetup = true
	full.Scripts = map[string]string{"seed": "bin/seed"}
	full.DiskWarn = &disabled
	full.DiskWarnPercent = -1
	full.DiskWarnGB = 20

	// Values that YAML would read as a number, bool or null unless quoted.
	scalars := DefaultConfig()
	scalars.MainBranch = "2024"
	scalars.Editor = "true"
	scalars.Setup = []string{"true", "null", "1.5", "- x"}
	scalars.Scripts = map[string]string{"123": "~"}

	for name, cfg := range map[string]*Config{
		"defaults": nil,
		"bare":     &bare,
		"checkout": &checkout,
		"full":     &full,
		"scalars":  &scalars,
	} {
		t.Run(name, func(t *testing.T) {
			out := renderAnnotatedConfig(cfg, "dev")
			if err := schema.Validate(yamlToJSON(t, out)); err != nil {
				t.Errorf("generated config does not validate:\n%v", err)
			}
			if cfg == nil {
				return
			}
			got, err := Parse([]byte(out))
			if err != nil {
				t.Fatal(err)
			}
			if got.MainBranch != cfg.MainBranch || got.Editor != cfg.Editor || !slices.Equal(got.Setup, cfg.Setup) || !maps.Equal(got.Scripts, cfg.Scripts) {
				t.Errorf("values changed on round-trip:\n%s", out)
			}
		})
	}

	// Parse treats a null value as omitted, e.g. a list whose items are all
	// commented out.
	if err := schema.Validate(yamlToJSON(t, "version: 1\nworktree_dir:\nsetup:\n#  - npm install\nscripts:\n")); err != nil {
		t.Errorf("schema rejected null values wtx accepts:\n%v", err)
	}

	if err := schema.Validate(yamlToJSON(t, "version: 1\nunknown_key: true\n")); err == nil {
		t.Error("schema accepted an unknown top-level key")
	}
}

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	abs, err := filepath.Abs(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.NewCompiler().Compile(abs)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaPath, err)
	}
	return schema
}

// yamlToJSON decodes YAML into the JSON values the validator accepts.
func yamlToJSON(t *testing.T, doc string) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return out
}
