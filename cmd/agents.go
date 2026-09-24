package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const agentsMarkdown = `# AGENTS.md — AI Workflow Guide for wtx

## Overview

wtx is a CLI for git worktree-based development. It manages isolated worktrees
under a worktrees/ directory with shared config files, symlinks, and template
variable substitution. Projects can be created via wtx clone (bare repo) or
wtx init (existing repo).

## Important: Non-Interactive Usage

wtx is interactive by default — commands launch pickers when arguments are
omitted. AI agents MUST always pass explicit arguments to avoid interactive
prompts.

    # Wrong (launches interactive picker):
    wtx add
    wtx remove
    wtx cd

    # Correct (explicit arguments):
    wtx add feature/auth
    wtx remove feature/auth --force
    wtx cd feature/auth

## Project Structure

After wtx clone (bare repo):

    project/
      .bare/              # Bare git repository (no .git at root)
      .worktree.yml       # Project configuration
      bin/
        refresh           # Starter script for 'wtx run refresh' (a commented no-op)
      shared/
        copy/             # Files copied into each new worktree
        symlink/          # Directories symlinked into each new worktree
      worktrees/
        main/             # Each branch gets its own directory
        feature-auth/

After wtx init (existing repo):

    project/
      .git/               # Existing git directory (project root is the main worktree)
      .worktree.yml       # Project configuration
      .worktrees/
        bin/
          refresh         # Starter script for 'wtx run refresh' (a commented no-op)
        shared/
          copy/           # Files copied into each new worktree
          symlink/        # Directories symlinked into each new worktree
        feature-auth/     # Additional worktrees live here

## Command Reference

### Clone a project (bare repo)

    wtx clone <url> [name]
    wtx clone <url> --dry-run          # Preview without executing

### Initialize in an existing repo

    wtx init                           # Run from the repo root
    wtx init --dry-run                 # Preview without executing

### Create a worktree

    wtx add <branch>                   # Detects remote or creates new branch

### List worktrees

    wtx list

### Remove a worktree

    wtx remove <name> --force          # Use --force to skip confirmation

### Get worktree path

    wtx cd <name>                      # Prints path to stdout (does NOT cd)

### Navigate to a worktree

    cd "$(wtx cd <name>)"              # Use shell substitution to cd

### Run a project script

    wtx run <name> [args...]           # Runs scripts.<name> from .worktree.yml in the current worktree
    wtx run --dry-run <name>           # wtx flags must precede the name; later args go to the script

bin/refresh is a generated stub. When asked to set up an environment refresh,
read it and implement the commented steps for this project's stack. Scripts
receive WTX_PROJECT_ROOT, WTX_SHARED_PATH, WTX_MAIN_BRANCH, WTX_MAIN_WORKTREE_PATH,
WTX_WORKTREE_PATH, WTX_BRANCH_NAME, WTX_WORKTREE_ID, and WTX_SCRIPT_NAME.
Deprecated WT_ aliases are also exported for compatibility.

### Apply shared files

    wtx apply <name>                   # Apply to one worktree
    wtx apply --all                    # Apply to all worktrees

### Open in editor

    wtx open <name>

### Show status of all worktrees

    wtx status

### Check health and migration readiness

    wtx doctor --json                 # Read-only project report
    wtx doctor --fix --dry-run        # Preview safe repairs
    wtx doctor --fix                  # Apply repairs and check resulting state
    wtx doctor --strict               # Warnings also cause exit code 1
    wtx doctor --user                 # User configuration only, works outside projects

Doctor repairs only Git compatibility config, managed exclusions, dead setup
records, and recognized existing Claude hooks. Repairs preserve permissions,
back up to the Git directory, and refuse inputs changed since inspection.
Review manual remedies for shared copies/links, registrations, scripts, branches,
and dotfiles. User dotfile changes are manual; --user --fix is rejected.
Dead setup records become failed, so review retained logs before running setup.
Exit 1 means failures, inspection errors, unsuccessful repairs, or strict warnings.
After repairs, exit status reflects the rechecked state; previews use current state.

The bounded rename scan checks project configuration, configured scripts, bin/,
shared text, root agent instructions, and tracked worktree files. It skips Git
internals, dependency/build directories, symlinks, binaries, and files over 1 MiB.
Treat text matches as review candidates. User scanning honors ZDOTDIR and
XDG_CONFIG_HOME without sourcing startup files or following arbitrary includes.
Replace WT_THEME and WT_NO_DISK_WARN input settings before v0.12; replace wt
commands and WT_* script exports before v1.0. Installing wtx migrates nothing.

### Fetch and pull all worktrees

    wtx sync                           # Pull all clean worktrees
    wtx sync --rebase                  # Use rebase instead of merge

### Remove worktrees with merged branches

    wtx prune --yes                    # Use --yes to skip confirmation
    wtx prune --force --yes            # Also remove merged worktrees with uncommitted changes

### Preview any command safely

    wtx --dry-run <command> [args]

### Configuration management

    wtx config init                    # Generate annotated .worktree.yml
    wtx config init --update           # Preserve existing values

## Common Workflows

### Starting a new project (clone)

    wtx clone git@github.com:org/repo.git
    cd repo
    wtx add feature/my-feature
    cd "$(wtx cd feature/my-feature)"

### Adding wtx to an existing repo

    cd existing-repo
    wtx init
    wtx add feature/my-feature
    cd "$(wtx cd feature/my-feature)"

### Creating a feature branch

    wtx add feature/my-feature
    cd "$(wtx cd feature/my-feature)"

### Checking project state

    wtx status
    wtx list

### Cleaning up after merge

    wtx sync
    wtx prune --yes

### Applying shared file changes

    wtx apply --all

## Configuration (.worktree.yml)

    version: 1
    git_dir: .bare                    # .bare for clone, .git for init
    worktree_dir: worktrees
    main_branch: main                 # auto-detected at clone/init
    editor: cursor
    setup:
      - "npm install"
    parallel_setup:
      - "bundle install"
    teardown:
      - "docker compose down"
    parallel_teardown:
      - "make clean"

Fields:
- version: Config version (always 1)
- git_dir: Path to git directory (.bare for cloned, .git for initialized)
- worktree_dir: Directory for worktrees (default: worktrees)
- main_branch: Primary branch, branch ref protected from deletion and used as base for new branches (default: main)
- editor: Preferred editor binary name (default: auto-detect)
- setup: Commands run sequentially after creating a worktree
- parallel_setup: Commands run concurrently after setup completes
- teardown: Commands run sequentially before removing a worktree
- parallel_teardown: Commands run concurrently after teardown completes

## Template Variables

Files in shared/copy/ ending in .template get variable substitution, with the
.template suffix stripped from the output filename. All other files are copied
as-is (no scanning, no substitution).

Example: shared/copy/.env.template → worktrees/feature-auth/.env

Available variables:

- ${WORKTREE_ID} — branch lowercased with / replaced by - (e.g. feature-auth)
- ${WORKTREE_PATH} — absolute path to the worktree
- ${BRANCH_NAME} — original branch name (e.g. feature/Auth)

## Key Caveats

1. wtx cd prints a path — it does not change directory. Always use:
   cd "$(wtx cd <name>)"
2. For cloned projects, there is no .git at the project root (bare repo at .bare/).
   For initialized projects, .git exists and the project root is the main worktree.
3. Use --force with wtx remove and --yes with wtx prune to skip interactive confirmation.
   wtx prune --force removes merged worktrees even when they have uncommitted changes.
4. Use --dry-run to safely preview any destructive operation.
5. The project root is identified by .worktree.yml — look for this file.
6. Run git commands inside the worktree directory, not the project root.
7. Worktree directories live at worktrees/<branch-name>/ under the project root.
`

func newAgentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "Print AI agent workflow instructions",
		Long:  "Outputs workflow instructions for AI tools to understand how to use wtx effectively.\nPipe to a file to create an AGENTS.md: wtx agents > AGENTS.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(agentsMarkdown)
			return nil
		},
	}
}
