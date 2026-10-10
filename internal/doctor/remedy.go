package doctor

import "github.com/bkildow/wtx/internal/ui"

// Every remedy that prints a command builds it here, quoting interpolated
// paths with ui.ShellQuote so it can be pasted into a POSIX shell as is.

// pruneRemedy asks the user to preview and then prune stale registrations.
func pruneRemedy(gitDir string) string {
	c := pruneCommand(gitDir)
	return "Review " + c + " --dry-run, then " + c + "."
}

func pruneCommand(gitDir string) string {
	return "git --git-dir=" + ui.ShellQuote(gitDir) + " worktree prune"
}

// unlockRemedy asks the user to unlock a worktree --migrate-home skipped.
func unlockRemedy(path string) string {
	return "Unlock it with " + unlockCommand(path) + ", then rerun wtx doctor --migrate-home."
}

func unlockCommand(path string) string {
	return "git worktree unlock " + ui.ShellQuote(path)
}
