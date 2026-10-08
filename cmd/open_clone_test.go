package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

func TestOpenCloneOutsideProjectExplains(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	orig := ui.Output
	ui.Output = &buf
	t.Cleanup(func() { ui.Output = orig })

	_, err := openClone(context.Background())
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Fatalf("err = %v, want ErrConfigNotFound", err)
	}
	if !strings.Contains(buf.String(), "Not a wtx project") || !strings.Contains(buf.String(), "wtx clone") {
		t.Errorf("output = %q", buf.String())
	}
}
