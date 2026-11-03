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
- **Dry-run is a first-class concept.** The global `--dry-run` flag is threaded through `git.Runner` (skips execution, prints what would happen) and `project.CreateScaffold`. Every command must leave the filesystem unchanged under `--dry-run`: `e2e/testdata/dry_run_sweep.txtar` runs each one between `snapshot` and `unchanged`.
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
3. Open the Clone with `openClone(ctx)` (from the current directory; prints a friendly error outside a project), `project.OpenAt(ctx, root, project.Options{...})` for a Project root the caller already knows (it skips root detection), or `project.Open(ctx, dir, project.Options{...})` to find the root from another directory or for silent shell completion. Use its accessors (`Root()`, `Config()`, `Runner()`, `WorktreesDir()`, `SharedDir()`, `ManagedWorktrees(ctx)`, `Worktrees(ctx)`) instead of raw `config.Load()`, `git.NewRunner()` or path helpers; the Clone resolves the per-clone `~/.wtx/<name>` paths and threads `--dry-run` into its Runner (queries such as `WorktreeList` still run under dry-run)
4. Add `exec wtx --dry-run <name> ...` and `unchanged` to `e2e/testdata/dry_run_sweep.txtar`, setting up whatever state the command acts on before the `snapshot` line
5. Document agent-relevant usage in `cmd/agent_skill.md` (the agent skill printed by `wtx skill`; it must not be named `SKILL.md` in any case, or `npx skills add` installs it instead of the wrapper). Keep its frontmatter identical to the installable wrapper in `skills/wtx/SKILL.md`; `cmd/skill_test.go` enforces this.

### Finishing work

When a change is complete and `make dev` passes, ship it as a PR without waiting to be asked:

1. Run `/simplify`, then re-run `make dev`.
2. Run `/code-review --fix`, then re-run `make dev`. Both passes stay inside the agreed plan: a fix that changes behavior other commands share, or that a review marked as needing the user's call, gets listed for the user instead of applied, or called out in the PR body if it was.
3. Commit on the current feature branch. Never commit to `main`; if you are on it, branch first.
4. Push with `git push -u origin HEAD`.
5. Invoke the `mattpocock-skills:pr` skill to write the body, then open the PR with `gh pr create`. If the branch already has a PR, push and update its body instead (`gh pr edit`).
6. Give the user the PR URL. Leave merging to them.

Bead IDs are local-only, so keep them out of commit messages and PR bodies (see `docs/agents/issue-tracker.md`).

### Rules and what enforces them

A failing check names what to use instead. When the user corrects a mistake, fix it and add its rule here; if the rule is already here with nothing enforcing it, enforce it in the same change. Drop a rule once its mistake can no longer happen.

| Rule | Enforced by |
| --- | --- |
| `--dry-run` changes nothing, for every command | `e2e/testdata/dry_run_sweep.txtar`; `cmd/dryrun_coverage_test.go` fails for a command the sweep does not run |
| Commands get config, Runner and root from a Clone, not `config.Load`, `config.Exists`, `git.NewRunner` or `project.FindRoot` | `cmd/clone_access_test.go`; lines that run before a Clone exists end in `// raw-load: <reason>` |
| Only `internal/config` and `internal/project` write config path fields; others read a Clone's resolved dirs | `internal/project/pathfields_test.go` |
| Git runs only through `Runner.command` or `gitAt` | `internal/git/exec_ratchet_test.go` (ratchet: no new hand-built git commands) |
| One `git worktree list` per command | `cmd/worktree_list_count_test.go` |
| Tests never see the real HOME, `~/.wtx`, git config or an enclosing wtx/git environment | `internal/testenv.Main` in every package's `TestMain` (`testenv.Isolate` in e2e), required by `internal/testenv/coverage_test.go`; `Main` also fails a run that writes under the shared home |
| The agent skill is never named `SKILL.md`; its frontmatter matches `skills/wtx/SKILL.md` | `cmd/skill_test.go` |
| A command that loops over worktrees, branches or batches reports a failed item and carries on (an error row or warning), so one bad worktree never aborts the rest | Judgment, nothing enforces it |
| `/simplify` and `/code-review --fix` run before every commit and stay inside the plan | Judgment, nothing enforces it |
| Reuse the existing helper (`ui.ShellQuote`, `project.SamePath`, `runTeardownHooks`, `newBranchStartPoint`...) instead of writing a second one | Judgment; `/simplify` looks for it |
| Never commit to `main`; no bead IDs in commits or PRs | Judgment, nothing enforces it |

---

## Agent skills

### Issue tracker

Issues live in beads (`br`), local-only since `.beads/` is gitignored. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`) applied as beads labels. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
