package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points HOME and WTX_HOME at a scratch directory so no test reads
// or writes the real ~/.wtx (doctor scans it for orphaned projects).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wtx-doctor-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("WTX_HOME", filepath.Join(dir, "wtx-home"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
