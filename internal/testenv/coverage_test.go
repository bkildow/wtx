package testenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// callsTestenv matches a TestMain that isolates the binary.
var callsTestenv = regexp.MustCompile(`\b(testenv\.)?(Main\(m\)|Isolate\(\))`)

// TestEveryTestPackageIsolated fails for a package whose tests could reach
// the real home directory, ~/.wtx or git config.
func TestEveryTestPackageIsolated(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	hasTests := map[string]bool{}
	isolated := map[string]bool{}
	err = filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != moduleRoot && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir, _ := filepath.Rel(moduleRoot, filepath.Dir(path))
		hasTests[dir] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "func TestMain(") && callsTestenv.Match(data) {
			isolated[dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir := range hasTests {
		if !isolated[dir] {
			t.Errorf("%s has tests but no TestMain calling testenv.Main; add %s/main_test.go with: func TestMain(m *testing.M) { testenv.Main(m) }", dir, dir)
		}
	}
}
