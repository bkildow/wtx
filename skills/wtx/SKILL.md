---
name: wtx
description: Manage git worktrees with the wtx CLI — create, list, switch to, sync, prune, or remove worktrees, run project scripts, and check project health. Use when working inside a wtx project (a directory tree containing .worktree.yml) or when the user mentions wtx.
---

# wtx

This skill is a thin wrapper. The full instructions ship inside the `wtx` binary so they always match the installed version.

1. Check that `wtx` is installed: `command -v wtx`. If it is missing, tell the user and stop. It installs with `brew install bkildow/tap/wtx` or `go install github.com/bkildow/wtx/cmd/wtx@latest`.
2. Run `wtx skill` and follow its output as the authoritative instructions.

Always pass explicit arguments (`wtx add <branch>`, `wtx cd <name>`, `wtx remove <name> --force`). Bare `wtx add`, `wtx cd`, and `wtx remove` open interactive pickers that an agent cannot answer.
