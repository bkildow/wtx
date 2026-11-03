package testenv

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// isolatingMain matches a TestMain that isolates the binary.
var isolatingMain = regexp.MustCompile(`(?m)^func TestMain\(m \*testing\.M\) \{[^}]*\b(testenv\.)?(Main\(m\)|Isolate\(\))`)

// TestEveryTestPackageIsolated fails for a package whose tests could reach
// the real home directory, ~/.wtx or git config.
func TestEveryTestPackageIsolated(t *testing.T) {
	hasTests := map[string]bool{}
	isolated := map[string]bool{}
	GoFiles(t, func(rel string, data []byte) {
		if !strings.HasSuffix(rel, "_test.go") {
			return
		}
		dir := path.Dir(rel)
		hasTests[dir] = true
		if isolatingMain.Match(data) {
			isolated[dir] = true
		}
	})
	for dir := range hasTests {
		if !isolated[dir] {
			t.Errorf("%s has tests but no TestMain calling testenv.Main; add %s/main_test.go with: func TestMain(m *testing.M) { testenv.Main(m) }", dir, dir)
		}
	}
}
