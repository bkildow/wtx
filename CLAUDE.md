# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make                # Build wtx and the deprecated wt shim
make test           # Run all tests
make test-short     # Skip integration tests
make vet            # Lint (golangci-lint)
make fmt            # Format code (gofumpt)
make dev            # fmt + vet + test + build
make clean          # Remove both built binaries
make install        # Install wtx and the deprecated wt shim to $GOBIN
```

Raw `go` commands for reference:

```bash
go build -o wtx ./cmd/wtx                   # Build binary
go install ./cmd/wtx ./cmd/wt              # Install wtx and the deprecated wt shim
go test ./...                             # Run all tests
go test ./internal/git/                   # Run tests for a single package
go test ./internal/git/ -run TestDryRun   # Run a specific test
go test -short ./...                      # Skip integration tests (git_test.go has integration tests gated on -short)
go vet ./...                              # Lint
```

## Architecture

`wtx` is a CLI tool for managing git worktree-based development workflows. It wraps a bare git repository and creates worktrees under a `worktrees/` directory with shared files/symlinks.

### Key design decisions

- **No `.git` at project root.** The bare repo lives at `.bare/` (configurable via `.worktree.yml`). All git operations go through `git.Runner` which passes `--git-dir` to every command.
- **`shared/` folder IS the config** for copy/symlink behavior. The directory structure mirrors the worktree root — no lists to maintain in YAML.
- **Dry-run is a first-class concept.** The global `--dry-run` flag is threaded through `git.Runner` (skips execution, prints what would happen) and `project.CreateScaffold`. New commands must respect `cmd.IsDryRun()`.
- **Interactive by default.** Commands that accept `[<name>]` should launch a huh picker when called without an argument. The `ui.Prompter` interface exists for testability.

### Package responsibilities

- **`cmd/`** — Cobra commands. Each command in its own file, registered in `root.go` init(). Global flags (like `--dry-run`) live on `rootCmd`.
- **`internal/git/`** — All git operations. `Runner` wraps `--git-dir` for bare repo context. `CloneBare` is the only method that bypasses `--git-dir` (it creates the bare repo). Parse functions (`parseRemoteBranches`, `parseWorktreeList`) are pure and unit-testable.
- **`internal/config/`** — `.worktree.yml` reading/writing. `DefaultConfig()` provides sensible defaults. `config.Exists()` and `config.Load()` are used by `project.FindRoot()` to walk up the directory tree.
- **`internal/project/`** — Project-level operations: root detection (walks up looking for `.worktree.yml`), scaffold creation, repo name extraction from URLs.
- **`internal/ui/`** — Terminal output (`output.go` with styled helpers) and interactive prompts (`prompts.go` with huh). All output goes to `ui.Output` (defaults to stderr) so stdout stays clean for machine-readable output like `wtx cd`.

### Adding a new command

1. Create `cmd/<name>.go` with `func new<Name>Cmd() *cobra.Command`
2. Register it in `cmd/root.go` `init()` via `rootCmd.AddCommand(new<Name>Cmd())`
3. Load project config with `config.Load()` and create a `git.NewRunner()` using the resolved git dir
4. Use `project.FindRoot()` to locate the project root from the current directory (for commands that run inside a project, unlike `clone`)

### Implementation roadmap (from ideas.md)

Phase 1 (done): scaffold, `wtx clone`. Phase 2 (next): `wtx add`, `wtx list`, `wtx remove`, `wtx cd`. Phase 3: `wtx apply`. Phase 4+: hooks, templates, completions, IDE integration.

<!-- br-agent-instructions-v1 -->

---

## Beads Workflow Integration

This project uses [beads_rust](https://github.com/Dicklesworthstone/beads_rust) (`br`/`bd`) for issue tracking. Issues are stored in `.beads/` and tracked in git.

### Essential Commands

```bash
# View ready issues (unblocked, not deferred)
br ready              # or: bd ready

# List and search
br list --status=open # All open issues
br show <id>          # Full issue details with dependencies
br search "keyword"   # Full-text search

# Create and update
br create --title="..." --description="..." --type=task --priority=2
br update <id> --status=in_progress
br close <id> --reason="Completed"
br close <id1> <id2>  # Close multiple issues at once

# Sync with git
br sync --flush-only  # Export DB to JSONL
br sync --status      # Check sync status
```

### Workflow Pattern

1. **Start**: Run `br ready` to find actionable work
2. **Claim**: Use `br update <id> --status=in_progress`
3. **Work**: Implement the task
4. **Complete**: Use `br close <id>`
5. **Sync**: Always run `br sync --flush-only` at session end

### Key Concepts

- **Dependencies**: Issues can block other issues. `br ready` shows only unblocked work.
- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog (use numbers 0-4, not words)
- **Types**: task, bug, feature, epic, chore, docs, question
- **Blocking**: `br dep add <issue> <depends-on>` to add dependencies

### Session Protocol

**Before ending any session, run this checklist:**

```bash
git status              # Check what changed
br sync --flush-only    # Export beads changes to JSONL
```

### Best Practices

- Check `br ready` at session start to find available work
- Update status as you work (in_progress → closed)
- Create new issues with `br create` when you discover tasks
- Use descriptive titles and set appropriate priority/type
- Always sync before ending session

<!-- end-br-agent-instructions -->
