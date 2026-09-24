package cmd

import (
	"fmt"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/disk"
	"github.com/bkildow/wtx/internal/ui"
)

// warnLowDisk checks free space on the project partition and, when it is low,
// warns and points at 'wtx prune'. It never returns an error: a failed statfs
// must not block the command that called it. Safe under --dry-run, since a
// statfs has no side effects.
func warnLowDisk(projectRoot string, cfg *config.Config) {
	if config.LookupEnv("NO_DISK_WARN") != "" {
		return
	}
	if cfg.DiskThreshold() == nil {
		return
	}

	usage, err := disk.Stat(projectRoot)
	if err != nil {
		ui.Command("statfs " + projectRoot + ": " + err.Error())
		return
	}

	msgs := lowDiskMessages(usage, cfg)
	if len(msgs) == 0 {
		return
	}

	// First line is the warning itself; the rest are follow-up guidance.
	ui.Warning(msgs[0])
	for _, msg := range msgs[1:] {
		ui.Step(msg)
	}
}

// lowDiskMessages returns the warning lines for the given usage, or nil when
// free space is above the configured thresholds (or warnings are disabled).
func lowDiskMessages(usage disk.Usage, cfg *config.Config) []string {
	threshold := cfg.DiskThreshold()
	if threshold == nil || !threshold.IsLow(usage) {
		return nil
	}

	msgs := []string{fmt.Sprintf("Low disk space: %s free of %s (%.0f%% free)",
		ui.FormatBytes(usage.FreeBytes), ui.FormatBytes(usage.TotalBytes), usage.PercentFree())}

	msgs = append(msgs, "Run 'wtx prune' to remove worktrees for merged branches.")

	if len(cfg.Teardown) == 0 && len(cfg.ParallelTeardown) == 0 {
		msgs = append(msgs, "No teardown hooks are configured, so 'wtx prune' frees worktree "+
			"directories but not docker volumes or other external resources.")
	}

	return msgs
}
