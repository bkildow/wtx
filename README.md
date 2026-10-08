<div align="center">
  <img src="wtx.webp" alt="wtx - git worktree workflow manager" width="600">

  <br>

  <p>A CLI for managing git worktree-based development workflows.<br>
  Clone once as a bare repo, then spin up isolated worktrees per branch with shared config files, symlinks, and template variables.</p>

  <p>
    <a href="https://github.com/bkildow/wtx/releases/latest"><img src="https://img.shields.io/github/v/release/bkildow/wtx" alt="Latest Release"></a>
    <a href="https://github.com/bkildow/wtx/blob/main/LICENSE"><img src="https://img.shields.io/github/license/bkildow/wtx" alt="License"></a>
    <a href="https://github.com/bkildow/wtx"><img src="https://img.shields.io/github/go-mod/go-version/bkildow/wtx" alt="Go Version"></a>
  </p>
</div>

---

> ### Migrating from `wt` to `wtx` (v0.11.0)
>
> v0.11.0 is available through Homebrew and `go install` using the commands below.
>
> In v0.11.0, `wtx` is the primary command and a deprecated `wt` shim ships
> alongside it. Existing `wt` commands keep working and print a migration warning.
> Update your [shell startup line](#shell-integration) to use `wtx shell-init`.
> Scripts receive both `WTX_*` and legacy `WT_*` variables; settings read `WTX_*`
> first and fall back to `WT_*` when the new variable is unset.
>
> In v0.12.0, the shim warning becomes stronger and legacy `WT_*` settings stop
> being read (exports remain). In v1.0.0, the `wt` shim, cask, and `WT_*` exports
> are removed. `.worktree.yml` and `${PROJECT_ROOT}`, `${WORKTREE_ID}`,
> `${WORKTREE_PATH}`, and `${BRANCH_NAME}` template variables stay unchanged.

## Features

- **Bare-repo workflow** — no `.git` at project root; all worktrees live under `worktrees/`
- **Adopt an existing checkout** — `wtx init` keeps worktrees and shared files in `~/.wtx/<name>/`, outside the repository, and teammates' clones join with the same command
- **Shared files** — copy per-worktree configs or symlink heavy directories (node_modules, vendor) once
- **`.worktreeinclude`** — copy gitignored files such as `.env` from the main worktree into new worktrees
- **Reflink-aware copies** — near-instant copy-on-write clones on APFS, btrfs, and reflink-enabled XFS; transparent byte-copy fallback on other filesystems
- **Template variables** — `${PROJECT_ROOT}`, `${WORKTREE_ID}`, `${BRANCH_NAME}`, etc. substituted in `.template` files
- **Interactive by default** — branch/worktree pickers when arguments are omitted
- **Setup/teardown hooks** — run commands automatically when creating or removing worktrees
- **Claude Code integration** — automatic worktree creation/removal via Claude Code hooks
- **Agent skill** — teaches any skills-aware coding agent to drive `wtx` non-interactively ([install](#agent-skill))
- **Editor integration** — open worktrees in your preferred editor ($EDITOR, config, or auto-detect)
- **Shell completions** — tab-complete worktree names in bash, zsh, and fish
- **Dry-run support** — preview every destructive operation with `--dry-run`

## Project health and migration checks

```bash
wtx doctor                    # Inspect from the project root or a worktree (grouped summary)
wtx doctor --verbose          # List every finding, including passing checks
wtx doctor --fix --dry-run    # Preview safe repairs, without writes or backups
wtx doctor --fix              # Apply safe repairs, then inspect again
wtx doctor --json             # Structured report on stdout (schema_version: 1)
wtx doctor --strict           # Warnings also produce exit code 1
wtx doctor --user             # Inspect user settings, even outside a project
wtx doctor --migrate-home     # Move an in-repo clone to ~/.wtx/<name>/
```

Doctor checks Git compatibility, managed exclusions, setup state, existing Claude
hooks, missing shared copies and managed links, worktree registrations, local
orphaned branches, executable script targets, disk thresholds, Compose
projects without teardown hooks, the worktree directory and the owner marker of
`~/.wtx/<name>/`, in-repo worktree layouts, and orphaned `~/.wtx` directories
whose project is gone (reported, never deleted). It never runs project scripts or setup hooks,
fetches remotes, invokes Docker, deletes branches, or removes worktrees.
Shared copies are checked for existence, including rendered `.template` names;
different worktree-local contents are expected and are not treated as drift.

`--fix` repairs only Git compatibility configuration, managed exclusions, dead
setup-process records, and recognized existing Claude hook commands. It preserves
custom settings and hooks and adds no absent hooks. Absolute `wt` hook paths are
changed only when their sibling `wtx` is executable; bare commands require `wtx`
on PATH. Linked settings inside the project are deduplicated by resolved path;
external targets require manual attention.

`--migrate-home [--name <name>]` moves an `init --in-repo` clone's worktrees,
`shared/`, and `bin/` to `~/.wtx/<name>/`, writes the owner marker, records
`<name>` as `wtx.name` in the local Git config, retargets shared symlinks, runs
`git worktree repair`, and removes `worktree_dir`, `shared_dir`, and the
`scripts` entries for files in `bin/` from `.worktree.yml`, so the paths resolve
per clone and `wtx run` finds the scripts in `bin/` by name (the previous file
is backed up under the Git directory). `<name>` defaults to `wtx.name`, then to
the repository directory name. Run
it from the project root and preview it with `wtx --dry-run doctor
--migrate-home`. Locked worktrees, worktrees with submodules or running setup,
and Git-tracked shared files stay in place and are reported; re-running resumes
a partial migration. It cannot be combined with `--fix`, and bare/clone
projects are skipped. Afterwards `.worktrees/` can be dropped from `.gitignore`.

Modified files receive unique `*.wtx-backup-*` backups under the Git directory's
`wtx-doctor-backups/` (outside worktrees and `shared/`) without replacing previous
backups. Repairs preserve permissions, stage replacements atomically,
and refuse inputs that changed after inspection. A second successful run makes
no further changes. A reconciled dead setup process remains a failed setup:
review its preserved log before rerunning `wtx setup`. Partial repair failures
remain visible. `wtx repair` keeps its existing narrow Git compatibility purpose.

Shared copies/links, worktree pruning, branches, arbitrary scripts, and user
dotfiles receive findings and manual remedies only. Review the suggested
`wtx apply` or Git pruning commands before running them yourself. Compose findings
ask you to review external resource cleanup and configure suitable teardown hooks.
`--user --fix` is rejected because dotfile changes are manual.

The rename scan covers `.worktree.yml`, configured scripts, project `bin/`, shared
text configuration, root `AGENTS.md`/`CLAUDE.md`, and Git-tracked text files in
existing worktrees. It skips Git internals, dependency/build directories, symlinks,
binary/non-UTF-8 files, backups, and files larger than 1 MiB, and reports the skipped
count. In tracked application files, bare `wt` is only reported in shell-like files
(shebang, shell, Makefile, YAML, Markdown); `WT_*` names are reported everywhere
except `${WTX_X:-${WT_X}}`-style compatibility fallbacks. This is a bounded
candidate scan, not a shell parser or a complete audit.
Matches include paths, line numbers, and identifiers, never full source lines.
The user scan honors `ZDOTDIR` and `XDG_CONFIG_HOME`, checks standard Bash, Zsh, and
Fish startup files, and never sources them or follows arbitrary shell includes.

Replace legacy input settings `WT_THEME` and `WT_NO_DISK_WARN` with their `WTX_`
names **before v0.12**. Replace `wt` command calls, shell startup invocations,
and legacy `WT_*` script-variable references **before v1.0**. Installation alone
does not migrate binaries, shell initialization, or projects.

Exit code is 0 when no failures remain, or 1 for failures, inspection errors, or
unsuccessful repairs. `--strict` also returns 1 for remaining warnings. Actual
repairs evaluate the resulting state; previews evaluate the current state. Human
reports use stderr; JSON uses stdout and includes scope, sorted findings,
`planned`/`applied`/`failed` repair outcomes, backup paths, and severity counts.
The deprecated `wt doctor` shim supports the same command and retains its warning.

## Requirements

- **macOS or Linux.** Windows is not supported — `wtx` leans on POSIX process
  handling and runs setup hooks through a POSIX shell.
- **Go 1.25+** (for building from source)
- **Git 2.20+** (for `extensions.worktreeConfig` and `git config --worktree`)
  - Git 2.48+ additionally enables relative-path worktrees, making the project directory portable (move/rename without breaking `.git` pointers). On older git, worktrees still work — they just use absolute paths.

## Install

### Homebrew

```bash
brew install bkildow/tap/wtx
```

Or add the tap once, then install:

```bash
brew tap bkildow/tap
brew install wtx
```

### Go

```bash
go install github.com/bkildow/wtx/cmd/wtx@latest
```

Or build from source:

```bash
git clone https://github.com/bkildow/wtx.git
cd wtx
go build -o wtx ./cmd/wtx
# move wtx to somewhere in your $PATH
```

<a id="agent-skill"></a>

### Agent skill (optional)

Install the `wtx` skill so coding agents (Claude Code, Codex, Cursor, and others that support skills) know how to use `wtx`. Requires Node.js for `npx`:

```bash
npx skills add bkildow/wtx -g    # User-level, for every project
npx skills add bkildow/wtx       # Current project only
```

The installed skill is a thin wrapper that tells the agent to run `wtx skill`, so its instructions come from your installed `wtx` binary and stay current when you upgrade `wtx`. The wrapper itself rarely changes; refresh it with `npx skills update`.

Without `npx`, copy [`skills/wtx/`](skills/wtx/) into your agent's skills directory (for example `~/.agents/skills/wtx` or `~/.claude/skills/wtx`).

## Quick Start

```bash
# Clone a repo into a bare worktree project
wtx clone git@github.com:org/repo.git
cd repo

# Create a worktree for a feature branch
wtx add feature/auth

# Navigate to it (see Shell Integration below for cd support)
cd "$(wtx cd feature/auth)"

# See all worktrees
wtx list

# When done, clean up merged branches
wtx prune
```

## Commands

| Command | Description |
|---------|-------------|
| `wtx clone <url> [name]` | Clone a repo as a bare worktree project |
| `wtx init` | Initialize wtx in an existing git checkout, or set up a clone of one |
| `wtx add [branch]` | Create a new worktree for a branch |
| `wtx list` | List all worktrees |
| `wtx remove [name]` | Remove a worktree and its branch |
| `wtx setup [name]` | Run setup hooks on an existing worktree |
| `wtx run [name] [args...]` | Run a named project script from any worktree |
| `wtx cd [name]` | Print worktree path for shell navigation |
| `wtx root` | Print project root path for shell navigation |
| `wtx apply [name]` | Apply shared files to a worktree |
| `wtx open [name]` | Open a worktree in an IDE |
| `wtx status` | Show status of all worktrees |
| `wtx doctor` | Check project health and migration readiness |
| `wtx sync` | Fetch and pull all worktrees |
| `wtx prune` | Remove worktrees with fully merged branches |
| `wtx config init` | Generate annotated `.worktree.yml` with documentation |
| `wtx claude init` | Configure Claude Code hooks for automatic worktree management |
| `wtx skill` | Print the agent skill for AI coding agents |
| `wtx shell-init <shell>` | Print shell startup config (wrapper + completions) |
| `wtx completion <shell>` | Generate shell completion script |

<a id="wt-clone"></a>

### wtx clone

```bash
wtx clone <url> [name]        # Clone repo as bare worktree project
wtx clone <url> --dry-run     # Preview without executing
```

Clones as a bare repo and writes `.worktree.yml`. Optionally prompts to create an initial worktree.

<a id="wt-init"></a>

### wtx init

```bash
wtx init                      # Use ~/.wtx/<repo-dir-name>/
wtx init --name api-v2        # Use ~/.wtx/api-v2/
wtx init --in-repo            # Keep everything in .worktrees/ inside the repo
wtx --dry-run init            # Print every write it would make
```

Run `wtx init` at the root of an existing checkout (a directory with a `.git`
directory). The checkout becomes the main worktree. The only file added to the
working tree is `.worktree.yml`; commit it. Worktrees, shared files, and scripts
are machine-local and live in `~/.wtx/<name>/`, set up per clone:

```
myrepo/                      # Your existing checkout (the main worktree)
├── .git/                    # Local config holds wtx.name = myrepo
├── .worktree.yml            # Commit it: hooks, main_branch (no machine-local paths)
└── .worktreeinclude         # Optional, commit it: ignored files to copy

~/.wtx/myrepo/               # Machine-local, outside the repository
├── project.yml              # Marker: the repository that owns this directory
├── bin/
│   └── refresh              # Starter script: `wtx run refresh`
├── shared/
│   ├── copy/
│   └── symlink/
└── worktrees/
    └── feature/auth/
```

Keeping worktrees outside the checkout keeps them out of anything rooted at it:
Docker build contexts and bind mounts, IDE indexes and search results, file
watchers, and linters or test runners that discover nested projects.

`.worktree.yml` leaves `worktree_dir` and `shared_dir` out. For `git_dir: .git`
projects, omitted paths resolve per clone to `~/.wtx/<name>/worktrees` and
`~/.wtx/<name>/shared` (scripts in `~/.wtx/<name>/bin/`), so the committed file
names no machine-local directory. Explicit values in `.worktree.yml` override
this for every clone. Set `WTX_HOME` to keep project directories somewhere
other than `~/.wtx`.

`<name>` is the clone's `wtx.name` in its local Git config (`git config --local
wtx.name`, shared by all of the clone's worktrees and never committed), or the
repository's directory name when that is unset. `wtx init` records it: `--name`
picks it, otherwise an existing `wtx.name` or the directory name is kept.
Names must not be `.`, `..`, or contain path separators. If `~/.wtx/<name>`
already exists, `wtx init` reuses it only when its `project.yml` names the same
repository. A directory that belongs to another repository, or has no
`project.yml`, is refused with a suggestion to run `wtx init --name <name>`.
Two checkouts with the same directory name therefore need distinct names.

`wtx` commands find the project from the checkout, from any of its worktrees, or
from inside `~/.wtx/<name>/` (through `project.yml`).

<a id="joining-from-a-clone"></a>

#### Joining from a clone

A teammate who clones a repository with a committed `.worktree.yml` runs
`wtx init` in the clone. Because the config leaves the paths to each clone,
`wtx init` joins the project: it sets up `~/.wtx/<name>/` (marker, `shared/`,
`worktrees/`, `bin/refresh`) and records `wtx.name`, and leaves `.worktree.yml`
unchanged. Running it again on a set-up clone changes nothing. Until then,
`wtx add` refuses to create worktrees and says to run `wtx init`; `wtx doctor`
flags the clone as not set up. If another checkout on the machine already owns
`~/.wtx/<name>/`, pick another name with `wtx init --name <other>`.

A `.worktree.yml` with explicit `worktree_dir` and `shared_dir` (an `--in-repo`
project, or an older config with `~/.wtx/...` paths) is already complete, and
`wtx init` reports it as an existing project. For an explicit `~/.wtx/<name>`
directory that lacks `project.yml`, it points at `wtx doctor --fix`, which
records the clone as the owner.

`--in-repo` keeps worktrees, shared files, and scripts in `.worktrees/` inside
the repository and writes those paths into `.worktree.yml`
(`worktree_dir: .worktrees`, `shared_dir: .worktrees/shared`,
`scripts.refresh: .worktrees/bin/refresh`). Add `.worktrees/` to `.gitignore`,
or commit `.worktrees/shared/` and ignore only the worktrees. `--name` cannot be
combined with `--in-repo`. `wtx doctor` points in-repo projects at
`wtx doctor --migrate-home`, which moves them to `~/.wtx/<name>/`.

<a id="wt-config-init"></a>

### wtx config init

```bash
wtx config init               # Generate annotated .worktree.yml (backs up existing)
wtx config init --update      # Merge existing values into annotated template
```

Generates a `.worktree.yml` with documentation comments for every field. If a config already exists, it is backed up to `.worktree.yml.bak` first. Use `--update` to preserve your existing values while adding documentation comments.

<a id="wt-add"></a>

### wtx add

```bash
wtx add feature/auth          # Create worktree for branch
wtx add                       # Interactive branch picker
wtx add feature/auth --skip-setup  # Create worktree without running setup hooks
wtx add feature/auth --base-branch develop  # Start the new branch from develop
```

Detects whether the branch exists remotely or creates a new local branch. New branches start from `main_branch` unless `--base-branch` names another branch, tag, or commit (`origin/<name>` is preferred when it exists, unless your local branch has unpushed commits). `--base-branch` errors if the ref doesn't resolve or the branch already exists; without a branch argument it prompts for a new branch name. Copies [`.worktreeinclude`](#worktreeinclude) files, applies shared files, and runs setup hooks. If setup hooks fail, the worktree is still created and you are CDed into it. Use `wtx setup [name]` later to bootstrap a worktree created with `--skip-setup`.

<a id="wt-remove"></a>

### wtx remove

```bash
wtx remove feature/auth       # Remove worktree and branch
wtx remove --force            # Skip uncommitted changes check
wtx remove feature/auth --skip-teardown  # Remove without running teardown hooks
```

Runs teardown hooks before removing the worktree directory.

<a id="wt-setup"></a>

### wtx setup

```bash
wtx setup feature/auth        # Run setup hooks on an existing worktree
wtx setup .                   # Target the worktree containing $PWD
wtx setup                     # Interactive picker
wtx setup --background        # Run hooks in the background
```

Re-runs the `setup:` and `parallel_setup:` hooks from `.worktree.yml` against an
existing worktree. The primary use case is bootstrapping a worktree that was
created with `wtx add --skip-setup`, but it can also be used to re-run hooks
after editing `.worktree.yml`. Refuses to run when a setup is already in
progress for the target worktree (check with `wtx status`).

<a id="wt-run"></a>

### wtx run

```bash
wtx run refresh               # Run the script named "refresh"
wtx run refresh --no-cache    # Everything after the name is passed to the script
wtx run                       # Interactive picker
wtx run --dry-run refresh     # Show what would run (wtx flags go before the name)
```

Runs a script from the `scripts:` map in `.worktree.yml`. Paths are resolved
relative to the project root, so a `bin/refresh-snapshot` that rebuilds your
local environment can be run identically from inside any worktree without
hunting for it. The script must exist and be executable.

A name without a `scripts:` entry runs the executable file of that name in the
project's `bin/` directory, the sibling of `shared_dir` (`bin/` for `wtx clone`,
`~/.wtx/<name>/bin/` for `wtx init`). A `scripts:` entry wins over a `bin/` file
of the same name. The picker and shell completion list both.

The script's working directory is the worktree containing `$PWD`, or the
current directory when run from outside a worktree. These environment
variables are exported:

| Variable | Value |
|----------|-------|
| `WTX_SCRIPT_NAME` | Name of the script being run |
| `WTX_PROJECT_ROOT` | Absolute project root |
| `WTX_SHARED_PATH` | Absolute shared directory (`copy/` and `symlink/` live here) |
| `WTX_MAIN_BRANCH` | `main_branch` from `.worktree.yml` (`main` when unset) |
| `WTX_MAIN_WORKTREE_PATH` | Main worktree: the project root for `wtx init` projects, otherwise the worktree checked out on the main branch (empty if none) |
| `WTX_WORKTREE_PATH` | Absolute path of the current worktree (empty outside a worktree) |
| `WTX_WORKTREE_ID` | Branch lowercased, `/` → `-` (empty outside a worktree) |
| `WTX_BRANCH_NAME` | Branch of the current worktree (empty outside a worktree) |

Each variable is also exported under its legacy `WT_*` name with the same value
through the transition. Prefer `WTX_*` in new scripts.

A non-zero exit from the script is reported as an error.

**Starter refresh script.** `wtx clone` and `wtx init` create `bin/refresh`
(`~/.wtx/<name>/bin/refresh` for `wtx init`, `.worktrees/bin/refresh` for
`wtx init --in-repo`). `wtx clone` and `wtx init --in-repo` register it as
`scripts.refresh`; `wtx init` relies on the `bin/` fallback, so the committed
config names no machine-local path.
It is a no-op that prints a message, but its comments lay out the typical
shape of an environment refresh: work in the main worktree via
`WTX_MAIN_WORKTREE_PATH`, start services, pull, refresh data, capture a
snapshot, and publish it under `WTX_SHARED_PATH/copy` so new worktrees inherit
it. Fill in the steps for your stack, or point an AI agent at the file and ask
it to.

<a id="wt-cd"></a>

### wtx cd

```bash
cd "$(wtx cd feature/auth)"   # Navigate to worktree
wtx cd                        # Interactive picker
```

Prints the absolute path to stdout. When run without a shell wrapper, `wtx cd` prints a hint about setting one up. See [Shell Integration](#shell-integration) for details.

<a id="wt-root"></a>

### wtx root

```bash
wtx root                      # Navigate to project root (with shell wrapper)
cd "$(wtx root)"              # Navigate without shell wrapper
```

Prints the absolute path to the project root (the directory containing `.worktree.yml`). With the shell wrapper, `wtx root` changes your directory directly.

<a id="wt-apply"></a>

### wtx apply

```bash
wtx apply feature/auth        # Apply shared files to one worktree
wtx apply --all               # Apply to all worktrees
```

Copies [`.worktreeinclude`](#worktreeinclude) files the worktree is missing, then copies files from `shared/copy/` (with template substitution) and creates symlinks from `shared/symlink/`. Shows each file copied and symlink created, with a summary count.

<a id="wt-open"></a>

### wtx open

```bash
wtx open feature/auth         # Open in editor
wtx open                      # Interactive picker
```

Editor resolution order: `editor` field in `.worktree.yml` > `$EDITOR` env var > auto-detect (Cursor, VS Code, Zed).

<a id="wt-status"></a>

### wtx status

```bash
wtx status
```

Shows branch, path, commit hash, dirty/clean status, and last commit age for all worktrees.

Also warns when the project's filesystem is running low on space — see [Low Disk Space Warnings](#low-disk-space-warnings).

<a id="wt-sync"></a>

### wtx sync

```bash
wtx sync                      # Fetch + pull all clean worktrees
wtx sync --rebase             # Use rebase instead of merge
```

Skips dirty worktrees. Shows summary of updated/skipped/failed counts.

<a id="wt-prune"></a>

### wtx prune

```bash
wtx prune                     # Remove worktrees with merged branches
wtx prune --force             # Also remove merged worktrees with uncommitted changes
wtx prune --yes               # Skip confirmation
```

Compares branches against the default branch (main/master). Detects regular, squash, and rebase merges, plus merged pull requests when `gh` is available. Merged worktrees with uncommitted changes are listed as `dirty` and kept unless you pass `--force`.

<a id="wt-skill"></a>

### wtx skill

```bash
wtx skill                     # Print the agent skill to stdout
```

Prints the full agent skill (a `SKILL.md` with frontmatter) that teaches AI coding agents how to use `wtx` non-interactively. The [installable skill](#agent-skill) is a thin wrapper that runs this command, so agents always get instructions that match the installed `wtx` version.

<a id="wt-claude-init"></a>

### wtx claude init

```bash
wtx claude init               # Configure Claude Code hooks
wtx claude init --binary /path/to/wtx  # Use a specific wtx binary path
```

Sets up [Claude Code hooks](https://docs.anthropic.com/en/docs/claude-code/hooks) so that Claude Code agents can create and remove worktrees automatically. Writes hook configuration to `shared/symlink/.claude/settings.local.json` and applies it to all existing worktrees via symlink.

This enables two hooks:
- **WorktreeCreate** — when Claude Code spawns a subagent with `--worktree`, `wtx` creates the worktree, copies `.worktreeinclude` files, applies shared files, and runs setup hooks. Claude Code skips `.worktreeinclude` when a WorktreeCreate hook is installed, so `wtx` applies it instead.
- **WorktreeRemove** — when the subagent finishes, `wtx` runs teardown hooks and cleans up the worktree and branch

Run `wtx claude init` once per project. The hooks propagate to all worktrees automatically.

<a id="wt-completion"></a>

### wtx completion

```bash
wtx completion bash > /etc/bash_completion.d/wtx
wtx completion zsh > "${fpath[1]}/_wtx"
wtx completion fish > ~/.config/fish/completions/wtx.fish
```

## How It Works

`wtx` organizes a project like this:

```
project/
├── .bare/                   # Bare git repository (no working tree)
├── .worktree.yml            # Project configuration
├── bin/
│   └── refresh              # Starter script for `wtx run refresh`
├── shared/
│   ├── copy/                # Files copied into each worktree
│   │   └── .env.example     # Supports ${TEMPLATE_VARS}
│   └── symlink/             # Shared resources symlinked from worktrees
│       ├── .claude/         # Claude Code hooks (via wtx claude init)
│       ├── node_modules/
│       └── vendor/
└── worktrees/
    ├── main/                # Each branch gets its own directory
    ├── feature-auth/
    └── feature-ui/
```

This is the layout `wtx clone` creates. `wtx init` adopts an existing checkout instead and keeps the same `bin/`, `shared/`, and `worktrees/` structure in `~/.wtx/<name>/`; see [wtx init](#wt-init).

**Why a bare repo?** Standard `git worktree` puts the primary checkout at the repo root, mixing repo files with worktree management. A bare repo at `.bare/` keeps the root clean — it only holds configuration and shared resources.

**Copy vs Symlink:** Files in `shared/copy/` are duplicated into each worktree (useful for `.env` files that vary per branch). Files in `shared/symlink/` are symlinked (useful for large directories like `node_modules` you only want to install once).

**Reflink copies:** On filesystems that support copy-on-write cloning — APFS on macOS, btrfs on Linux, and XFS formatted with `reflink=1` — `wtx` clones files in `shared/copy/` instead of reading and rewriting every byte. Clones share on-disk blocks with the source until one side is modified, so a 1 GB `vendor/` directory creates a new worktree in milliseconds and occupies no extra disk space. On other filesystems (ext4, NFS, tmpfs, cross-volume copies), `wtx` falls back to a normal byte-for-byte copy automatically — no configuration required. Docker bind mounts work fine with reflinked files.

## Configuration

### .worktree.yml

```yaml
version: 1
git_dir: .bare
main_branch: main
editor: cursor
setup:
  - "cp .env.example .env"
parallel_setup:
  - "npm install"
  - "bundle install"
teardown:
  - "docker compose down"
parallel_teardown:
  - "make clean"
  - "rm -rf tmp/"
scripts:
  refresh: bin/refresh-snapshot
  seed: bin/seed
```

| Field | Description | Default |
|-------|-------------|---------|
| `version` | Config version | `1` |
| `git_dir` | Path to the git directory (`.git` for `wtx init` projects) | `.bare` |
| `worktree_dir` | Directory holding worktrees (see [path rules](#path-rules)) | `worktrees`; `~/.wtx/<name>/worktrees` per clone for `git_dir: .git` |
| `shared_dir` | Directory holding `copy/` and `symlink/`; scripts live in a `bin/` next to it (see [path rules](#path-rules)) | `shared`; `~/.wtx/<name>/shared` per clone for `git_dir: .git` |
| `main_branch` | Primary branch (branch ref protected from deletion, used as base for new branches unless `wtx add --base-branch` is given) | `main` |
| `editor` | Preferred editor binary name | (auto-detect) |
| `setup` | Commands to run sequentially after creating a worktree | `[]` |
| `parallel_setup` | Commands to run concurrently after serial setup hooks | `[]` |
| `teardown` | Commands to run sequentially before removing a worktree | `[]` |
| `parallel_teardown` | Commands to run concurrently after serial teardown hooks | `[]` |
| `scripts` | Named executables for `wtx run <name>` (see [path rules](#path-rules)); executables in `bin/` run by name without an entry | `{}` |
| `disk_warn` | Warn when free disk space is low (`false` disables) | `true` |
| `disk_warn_percent` | Warn below this percentage of free space (`-1` disables this bound) | `10` |
| `disk_warn_gb` | Warn below this many GB of free space (`-1` disables this bound) | `10` |

`wtx init` writes `git_dir: .git` and leaves `worktree_dir` and `shared_dir`
out, so they resolve per clone to `~/.wtx/<name>/worktrees` and
`~/.wtx/<name>/shared`, where `<name>` is the clone's `git config wtx.name` or
the repository directory name (see [Joining from a clone](#joining-from-a-clone)).
With `--in-repo` it writes `worktree_dir: .worktrees` and
`shared_dir: .worktrees/shared`. Omitted paths of `git_dir: .bare` projects
default to `worktrees` and `shared`.

<a id="path-rules"></a>

**Path rules.** `worktree_dir`, `shared_dir`, and `scripts` paths resolve as follows:

| Form | Resolves to |
|------|-------------|
| `~/.wtx/...` | `$WTX_HOME/...` when `WTX_HOME` is set, otherwise `~/.wtx/...` |
| `~/...` | Your home directory |
| `/abs/path` | Used as-is |
| anything else | Relative to the project root |

`~user` forms are not expanded. Keep the literal `~` in `.worktree.yml` so the
file works on every machine. An explicit `~/.wtx/<name>/...` path names the same
directory for every clone; leave `worktree_dir` and `shared_dir` out to give
each clone its own.

### .worktreeinclude

`.worktreeinclude` lists gitignored files to copy from the main worktree into
each new worktree, such as `.env` files or local credentials. It uses gitignore
syntax and lives at the root of the main worktree. Claude Code and Conductor
read the same file. Commit it to the repository.

```gitignore
# .worktreeinclude
.env*
secrets/
```

- **Only ignored files are copied.** A file must match a `.worktreeinclude`
  pattern and be ignored by git. Tracked files come from git, so a tracked
  `.env.example` is never copied from the main worktree, even when it matches.
- **Source.** For `wtx init` projects the source is the project root (your
  checkout). For `wtx clone` projects it is the worktree checked out on
  `main_branch`, and `.worktreeinclude` is read from that worktree.
- **Seed only.** Existing files are never overwritten. Re-running `wtx apply`
  leaves a worktree's edited `.env` alone and copies only files the worktree is
  missing.
- **Apply order.** `wtx add`, `wtx apply`, and the `wtx claude init`
  WorktreeCreate hook apply `.worktreeinclude` first, then `shared/copy/`, then
  `shared/symlink/`. Later layers win, so a file in `shared/copy/` replaces the
  one copied from the main worktree.
- Files inside wtx's own directories (worktrees, `shared/`, `bin/`) are skipped.
  A missing `.worktreeinclude` is a no-op, and a failure to read it is a
  warning that does not block creating the worktree.
- `--dry-run` lists each file it would copy.

### Environment variables

| Variable | Effect |
|----------|--------|
| `WTX_HOME` | Directory for `wtx init` project directories and `~/.wtx/...` paths (default `~/.wtx`) |
| `WTX_NO_DISK_WARN` | Any non-empty value disables [low disk space warnings](#low-disk-space-warnings) |
| `WTX_THEME` | Color theme for terminal output |

Scripts run by `wtx run` also receive the [`WTX_*` variables](#wt-run) listed
under `wtx run`.

### Setup & Teardown Hooks

Hooks run in the worktree directory via `sh -c`. Serial hooks (`setup`/`teardown`) run sequentially; a failing hook is logged but does not prevent subsequent hooks from running.

- **Setup hooks** run after worktree creation and shared file application. If any hook fails, `wtx add` reports the error (the worktree is still created).
- **Teardown hooks** run before worktree removal. Hook failures are logged as warnings and do not prevent removal.
- Both respect `--dry-run` (prints what would run without executing).

### Parallel Hooks

Use `parallel_setup` and `parallel_teardown` for independent commands that can run concurrently (e.g., installing packages for different language ecosystems). Execution order:

1. Serial hooks run first (`setup` / `teardown`)
2. Parallel hooks run after serial hooks complete (`parallel_setup` / `parallel_teardown`)

All parallel commands start simultaneously and run to completion — a failing command does not cancel the others. Each command's output is prefixed with `[command]` to distinguish interleaved output. `--skip-setup` and `--skip-teardown` skip both serial and parallel hooks.

### Low Disk Space Warnings

Running many worktrees at once (each with its own containers, `node_modules`, DB volumes, and build caches) adds up quickly. `wtx add` and `wtx status` check free space on the project's filesystem and warn when it runs low:

```
⚠ Low disk space: 6.2 GB free of 460 GB (1% free)
→ Run 'wtx prune' to remove worktrees for merged branches.
→ No teardown hooks are configured, so 'wtx prune' frees worktree directories but not docker volumes or other external resources.
```

The check warns when free space is below **either** bound (`disk_warn_percent` or `disk_warn_gb`); set a bound to `-1` to disable it individually. The last line only appears when no `teardown`/`parallel_teardown` hooks are configured, since without them `wtx prune` cannot reclaim resources living outside the worktree directory.

Disable the warning permanently with `disk_warn: false` in `.worktree.yml`, or per-invocation with `WTX_NO_DISK_WARN=1`. The legacy `WT_NO_DISK_WARN`
setting is used only when `WTX_NO_DISK_WARN` is unset.

### Template Variables

Files in `shared/copy/` ending in `.template` get variable substitution, with the `.template` suffix stripped from the output filename. All other files are copied as-is.

Example: `shared/copy/.env.template` → `worktrees/feature-auth/.env`

| Variable | Derivation | Example (branch: `feature/Auth`) |
|----------|------------|----------------------------------|
| `${PROJECT_ROOT}` | Absolute project root path | `/path/to/project` |
| `${WORKTREE_ID}` | Branch lowercased, `/` → `-` | `feature-auth` |
| `${WORKTREE_PATH}` | Absolute worktree path | `/path/to/worktrees/feature/Auth` |
| `${BRANCH_NAME}` | Raw branch name | `feature/Auth` |

## Shell Integration

Add one line to your shell config to enable directory navigation (`wtx cd`) and tab completions:

**Bash** (`~/.bashrc`):

```bash
eval "$(wtx shell-init bash)"
```

**Zsh** (`~/.zshrc`):

```bash
eval "$(wtx shell-init zsh)"
```

**Fish** (`~/.config/fish/config.fish`):

```fish
wtx shell-init fish | source
```

This sets up `wtx` and deprecated `wt` wrapper functions so that `cd`, `root`,
`add`, and `remove` can change your directory, and registers tab completions
for both command names.

**Migrating an existing shell:** replace your old `wt shell-init` startup line
with the matching `wtx shell-init` line above, then start a new shell. Remove
any manually defined `wt` wrapper or alias so it does not override the generated
compatibility wrapper. Existing `wt cd` calls keep working during the transition.

### Manual Setup

If you prefer to configure the wrapper and completions separately, see `wtx shell-init <shell>` for the wrapper function source and `wtx completion <shell>` for standalone completion scripts.

## License

MIT
