package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/doctor"
)

func TestDoctorRealBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("real binary integration test")
	}
	bin := t.TempDir()
	for _, name := range []string{"wtx", "wt"} {
		c := exec.Command("go", "build", "-o", filepath.Join(bin, name), "./cmd/"+name)
		c.Dir = ".."
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", name, err, out)
		}
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "main", root}, {"-C", root, "commit", "--allow-empty", "-m", "initial"}} {
		c := exec.Command("git", args...)
		c.Env = gitEnv()
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	c := exec.Command(filepath.Join(bin, "wtx"), "init")
	c.Dir = root
	c.Env = append(gitEnv(), "WTX_NO_DISK_WARN=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("wtx init: %v: %s", err, out)
	}
	// Replace the starter's documented legacy fallback examples with a current
	// script, so this fixture is also clean under the candidate scan.
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, cfg.Scripts["refresh"]), []byte("#!/bin/sh\nexit 99\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wtx", "wt"} {
		run := func(dir string, wantExit int, args ...string) {
			t.Helper()
			c := exec.Command(filepath.Join(bin, name), args...)
			c.Dir = dir
			c.Env = append(gitEnv(), "WTX_NO_DISK_WARN=1", "WTX_THEME=definitely-invalid", "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
				t.Fatalf("%s %v exit %d want %d; out=%s err=%s", name, args, code, wantExit, stdout.String(), stderr.String())
			}
			var report doctor.Report
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("invalid JSON: %v: %s", err, stdout.String())
			}
			if report.SchemaVersion != 1 {
				t.Fatalf("unexpected schema: %+v", report)
			}
			if name == "wtx" && stderr.Len() != 0 {
				t.Fatalf("JSON diagnostics leaked to stderr: %s", stderr.String())
			}
			if name == "wt" && !strings.Contains(stderr.String(), "wt is deprecated") {
				t.Fatal("shim compatibility warning lost")
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
}
