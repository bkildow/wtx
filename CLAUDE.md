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
3. Load project config with `loadProject()` / `loadProjectAt()` (not raw `config.Load()`, which skips the per-clone `~/.wtx/<name>` path resolution) and create a `git.NewRunner()` using the resolved git dir
4. Use `project.FindRoot()` to locate the project root from the current directory (for commands that run inside a project, unlike `clone`)
5. Document agent-relevant usage in `cmd/agent_skill.md` (the agent skill printed by `wtx skill`; it must not be named `SKILL.md` in any case, or `npx skills add` installs it instead of the wrapper). Keep its frontmatter identical to the installable wrapper in `skills/wtx/SKILL.md`; `cmd/skill_test.go` enforces this.

---

## Agent skills

### Issue tracker

Issues live in beads (`br`), local-only since `.beads/` is gitignored. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`) applied as beads labels. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
