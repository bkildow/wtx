package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func TestConfigInitDryRunWritesNothing(t *testing.T) {
	previousDry := dryRun
	dryRun = true
	t.Cleanup(func() { dryRun = previousDry })

	for _, update := range []bool{false, true} {
		t.Run(fmt.Sprintf("update=%v", update), func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, config.ConfigFileName)
			original := "version: 1\ngit_dir: .bare\n"
			if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			buf := captureUI(t)

			command := newConfigInitCmd()
			if err := command.Flags().Set("update", fmt.Sprint(update)); err != nil {
				t.Fatal(err)
			}
			if err := runConfigInit(command, nil); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(buf.String(), "write "+configPath) {
				t.Errorf("missing dry-run write notice in output:\n%s", buf)
			}
			if got, err := os.ReadFile(configPath); err != nil || string(got) != original {
				t.Errorf("config changed under --dry-run: %q, %v", got, err)
			}
			if _, err := os.Stat(configPath + ".bak"); !os.IsNotExist(err) {
				t.Errorf("backup written under --dry-run (stat err %v)", err)
			}
		})
	}
}
