package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/disk"
	"github.com/bkildow/wtx/internal/ui"
)

func TestLowDiskMessages(t *testing.T) {
	const gb = disk.BytesPerGB

	low := disk.Usage{TotalBytes: 500 * gb, FreeBytes: 2 * gb}
	roomy := disk.Usage{TotalBytes: 500 * gb, FreeBytes: 400 * gb}
	disabled := false

	tests := []struct {
		name        string
		usage       disk.Usage
		cfg         config.Config
		wantLines   int
		wantCaveat  bool
		wantSummary string
	}{
		{
			name:      "plenty of space is silent",
			usage:     roomy,
			cfg:       config.DefaultConfig(),
			wantLines: 0,
		},
		{
			name:      "disabled by config",
			usage:     low,
			cfg:       config.Config{DiskWarn: &disabled},
			wantLines: 0,
		},
		{
			name:       "custom percent threshold triggers on a roomy disk",
			usage:      roomy,
			cfg:        config.Config{DiskWarnPercent: 99, DiskWarnGB: -1},
			wantLines:  3,
			wantCaveat: true,
		},
		{
			name:        "low with teardown hooks omits the caveat",
			usage:       low,
			cfg:         config.Config{Teardown: []string{"docker compose down -v"}},
			wantLines:   2,
			wantSummary: "2.0 GB free of 500 GB (0% free)",
		},
		{
			name:       "low with parallel teardown hooks omits the caveat",
			usage:      low,
			cfg:        config.Config{ParallelTeardown: []string{"make clean"}},
			wantLines:  2,
			wantCaveat: false,
		},
		{
			name:       "low without teardown hooks includes the caveat",
			usage:      low,
			cfg:        config.DefaultConfig(),
			wantLines:  3,
			wantCaveat: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			msgs := lowDiskMessages(tt.usage, &cfg)

			if len(msgs) != tt.wantLines {
				t.Fatalf("got %d message(s) %v, want %d", len(msgs), msgs, tt.wantLines)
			}
			if tt.wantLines == 0 {
				return
			}

			if !strings.HasPrefix(msgs[0], "Low disk space:") {
				t.Errorf("first message = %q, want a low-disk summary", msgs[0])
			}
			if tt.wantSummary != "" && !strings.Contains(msgs[0], tt.wantSummary) {
				t.Errorf("first message = %q, want it to contain %q", msgs[0], tt.wantSummary)
			}
			if !strings.Contains(msgs[1], "wt prune") {
				t.Errorf("second message = %q, want a 'wt prune' recommendation", msgs[1])
			}

			joined := strings.Join(msgs, "\n")
			hasCaveat := strings.Contains(joined, "No teardown hooks are configured")
			if hasCaveat != tt.wantCaveat {
				t.Errorf("teardown caveat present = %v, want %v (messages: %v)", hasCaveat, tt.wantCaveat, msgs)
			}
		})
	}
}

// TestWarnLowDiskEnvOverride verifies the escape hatch silences the warning
// even when the configured threshold would trigger.
func TestWarnLowDiskEnvOverride(t *testing.T) {
	var buf bytes.Buffer
	origOutput := ui.Output
	ui.Output = &buf
	t.Cleanup(func() { ui.Output = origOutput })

	// A 99% threshold triggers on any real filesystem.
	cfg := config.Config{DiskWarnPercent: 99, DiskWarnGB: -1}

	warnLowDisk(t.TempDir(), &cfg)
	if buf.Len() == 0 {
		t.Fatal("expected a warning without the env override")
	}

	buf.Reset()
	t.Setenv(diskWarnEnvVar, "1")
	warnLowDisk(t.TempDir(), &cfg)
	if buf.Len() != 0 {
		t.Errorf("expected no output with %s set, got %q", diskWarnEnvVar, buf.String())
	}
}
