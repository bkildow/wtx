---
name: wtx
description: wtx git-worktree CLI. Load before running any wtx command or answering questions about wtx output (doctor, add, list, remove, apply, etc.). Use whenever the user mentions wtx or worktrees, or when working inside a wtx project (a directory tree containing .worktree.yml).
---

# wtx

wtx runs one git worktree per branch, with shared files copied or symlinked into each and setup/teardown hooks run around it. Run `wtx <command> --help` for any command's full flags; this skill covers what help does not.

## Explicit arguments

Every command that takes a name opens an interactive picker when the name is omitted, and an agent cannot answer a picker. Always pass every argument, plus the flag that skips confirmation:

    wtx add feature/auth
    wtx cd feature/auth
    wtx remove feature/auth --force
    wtx prune --yes
    wtx run refresh

`--dry-run` previews any command. It is a global flag: `wtx --dry-run remove feature/auth --force`.

## Project layout

The project root is the directory containing `.worktree.yml`. Worktree directories use the branch name verbatim, so `feature/auth` lives at `worktrees/feature/auth/`.

Bare layout (`wtx clone <url>`), a bare repo with no `.git` at the root:

    project/
      .bare/              # bare git repository
      .worktree.yml
      bin/refresh         # starter script for `wtx run refresh`
      shared/
        copy/             # copied into each new worktree
        symlink/          # symlinked into each new worktree
      worktrees/
        main/
        feature/auth/

Initialized project (`wtx init` inside an existing repo), where the root is itself the main worktree and everything else lives in `~/.wtx/<name>/` (`$WTX_HOME/<name>/` when set):

    project/
      .git/               # local config: wtx.name = project
      .worktree.yml       # no worktree_dir/shared_dir: they default per clone
    ~/.wtx/project/
      project.yml         # marker: root of the owning repo
      bin/refresh
      shared/copy/
      shared/symlink/
      worktrees/
        feature/auth/

`<name>` is the clone's `git config wtx.name`, else the repo directory name. Each clone runs `wtx init` once to set up its `~/.wtx/<name>/`; in a clone whose committed `.worktree.yml` omits `worktree_dir`/`shared_dir`, `wtx init` only does that and leaves the config alone. If `wtx add` says the clone is not set up, run `wtx init`; if it says the directory belongs to another repo, run `wtx init --name <other>`. Explicit `worktree_dir`/`shared_dir` values override the per-clone default; an explicit `~/.wtx/...` directory that exists without `project.yml` makes `wtx add` refuse until `wtx doctor --fix` records the owner, and one that does not exist yet is created with its marker by `wtx add`.

`wtx init --in-repo` puts worktrees, `shared/` and `bin/` under `project/.worktrees/` instead. Find a project's worktrees with `wtx list` or `wtx cd <name>` rather than assuming a path.

Run git commands inside a worktree. In a Bare layout the project root has no `.git`, so git fails there.

## Paths and navigation

`wtx cd <name>` prints a path and changes nothing. Move with `cd "$(wtx cd <name>)"`. `wtx cd .` prints the current worktree and `wtx root` prints the project root.

## Creating a worktree

`wtx add <branch>` checks out the remote branch if one exists and otherwise creates a new branch from `main_branch` (override with `--base-branch <ref>`). It then applies shared files and runs setup hooks.

When `background_setup: true`, `wtx add` returns before setup finishes. Pass `--foreground` whenever you will build or test in the new worktree right away. The worktree is ready when `wtx cd <branch>` resolves and the SETUP column of `wtx status` reads Complete (or `-` when no hooks are configured). Failed means run `wtx logs <branch>` to read the setup output before working there. `wtx setup <name> --foreground` re-runs setup.

## Removing worktrees

- `wtx remove <name> --force` runs teardown hooks, then removes the worktree and its branch. A branch git calls unmerged is kept, and the output prints the exact `git --git-dir <dir> branch -D <name>` command to delete it. Run it only if the user wants those commits gone.
- `wtx prune --yes` removes every worktree whose branch is merged, including squash, rebase and merged-PR merges. Merged worktrees with uncommitted changes are kept unless you add `--force`. Git calls squash- and rebase-merged branches unmerged, so prune keeps those branches and prints the `branch -D` command for each. Their work is already in the default branch, so deleting them is safe.
- `--skip-teardown` skips teardown hooks on either command.

## Project scripts

`wtx run <name> [args...]` runs `scripts.<name>` from `.worktree.yml`, else the executable `<name>` in the project's `bin/` (next to `shared/`), in the current worktree. Every argument after the name goes to the script, so wtx flags go first: `wtx run --dry-run refresh`.

`bin/refresh` is a generated stub. When asked to set up an environment refresh, read it and implement its commented steps for this project's stack. `wtx run --help` lists the `WTX_*` environment variables scripts receive.

## Shared files and templates

Files under `shared/copy/` are copied into each new worktree, and entries under `shared/symlink/` are linked in. After changing them, run `wtx apply --all` (or `wtx apply <name>`) to update existing worktrees.

`.worktreeinclude` (gitignore syntax, committed at the repo root) lists ignored files such as `.env` to copy from the main worktree (the `main_branch` worktree in a Bare layout). Only files that match and are gitignored are copied, and existing files are never overwritten. Order: `.worktreeinclude`, then `shared/copy/`, then `shared/symlink/`; later layers win. To give worktrees a local secret, add its pattern to `.worktreeinclude` rather than copying it by hand.

Files ending in `.template` are copied with the suffix stripped and these variables substituted:

- `${WORKTREE_ID}`: the branch lowercased with `/` replaced by `-` (`feature/Auth` → `feature-auth`)
- `${WORKTREE_PATH}`: absolute path to the worktree
- `${BRANCH_NAME}`: the branch name verbatim

## .worktree.yml

    version: 1
    git_dir: .bare            # .git for initialized projects
    worktree_dir: worktrees   # relative, absolute, or ~/...; omitted with git_dir: .git → ~/.wtx/<name>/worktrees
    main_branch: main         # protected from deletion; default base for new branches
    editor: cursor
    setup: ["npm install"]            # sequential, after creating a worktree
    parallel_setup: ["bundle install"]    # concurrent, after setup
    teardown: ["docker compose down"]     # sequential, before removing a worktree
    parallel_teardown: ["make clean"]     # concurrent, after teardown
    background_setup: false
    scripts:                  # optional: bin/ executables also run by name
      refresh: bin/refresh    # relative to the project root

`wtx config init --update` rewrites the file with documentation comments and keeps existing values.

## Project health

- `wtx status` shows every worktree's branch, dirty state, and setup state, and names `wtx logs <branch>` for each failed background setup.
- `wtx logs <name>` prints a worktree's background setup log to stdout. Foreground setup prints to the terminal and keeps no log.
- `wtx doctor --json` reports project health and wt-to-wtx migration readiness, and changes nothing. `wtx doctor --help` covers repairs.
- `wtx doctor --migrate-home` moves an in-repo project (`init --in-repo`, or `worktrees/` and `shared/` at the repository root) to `~/.wtx/<name>/`. Only run it when the user asks; preview with `wtx --dry-run doctor --migrate-home`.
- `wtx repair` restores the per-worktree git config after a git or wtx upgrade. It is safe to re-run.
- `wtx sync` fetches and pulls every clean worktree (`--rebase` to rebase).
