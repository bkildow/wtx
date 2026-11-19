package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/doctor"
)

func TestDoctorRealBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("real binary integration test")
	}
	bin := buildWTX(t)
	root, wtxHome := realProject(t, bin)
	run := func(dir string, wantExit int, args ...string) {
		t.Helper()
		c := exec.Command(filepath.Join(bin, "wtx"), args...)
		c.Dir = dir
		c.Env = realEnv(wtxHome, "WTX_THEME=definitely-invalid", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		code := 0
		if err != nil {
			var e *exec.ExitError
			if errors.As(err, &e) {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != wantExit {
			t.Fatalf("wtx %v exit %d want %d; out=%s err=%s", args, code, wantExit, stdout.String(), stderr.String())
		}
		var report doctor.Report
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("invalid JSON: %v: %s", err, stdout.String())
		}
		if report.SchemaVersion != 1 {
			t.Fatalf("unexpected schema: %+v", report)
		}
		if stderr.Len() != 0 {
			t.Fatalf("JSON diagnostics leaked to stderr: %s", stderr.String())
		}
	}
	run(root, 0, "doctor", "--json", "--verbose")
	run(root, 0, "doctor", "--json", "--strict")
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(root, 0, "doctor", "--json")
	run(root, 1, "doctor", "--json", "--strict")
	if err := os.Remove(filepath.Join(root, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	run(t.TempDir(), 1, "doctor", "--json")
	run(t.TempDir(), 1, "doctor", "--json", "--user", "--fix")
}
