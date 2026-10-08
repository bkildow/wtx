---
name: wtx
description: wtx git-worktree CLI. Load before running any wtx command or answering questions about wtx output (doctor, add, list, remove, apply, etc.). Use whenever the user mentions wtx or worktrees, or when working inside a wtx project (a directory tree containing .worktree.yml).
---

# wtx

1. Check that `wtx` is installed: `command -v wtx`. If it is missing, tell the user it installs with `brew install bkildow/tap/wtx` or `go install github.com/bkildow/wtx/cmd/wtx@latest`, and stop.
2. Run `wtx skill`. Its output is the authoritative instructions for the installed version; follow it.
