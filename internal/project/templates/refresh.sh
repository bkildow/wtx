#!/usr/bin/env bash
#
# refresh — rebuild this project's shared local environment.
#
# PURPOSE
#   Bring the main worktree to a known-good state (fresh code, fresh data),
#   capture anything expensive to rebuild (a database dump, seeded fixtures,
#   built assets), and publish it into the shared/ directory so every new
#   worktree starts from it instead of rebuilding from scratch.
#
# HOW IT IS RUN
#   wtx run refresh [args...]
#
#   wtx runs this file from whichever worktree you are standing in and exports
#   the WTX_* variables listed below. You can also run it directly.
#
# STATUS
#   This is a generated starter. It currently does nothing except print a
#   message. Replace the commented example steps below with the real commands
#   for this project, then delete the "not implemented" block at the bottom.
#
# NOTE TO AI AGENTS
#   If you are asked to implement this script:
#     1. Inspect the main worktree ($WTX_MAIN_WORKTREE_PATH) for how the local
#        environment is defined: compose.yaml / docker-compose.yml, .ddev/,
#        Makefile, package.json, Procfile, devcontainer.json, etc.
#     2. Keep the step structure below. Fill each step with the project's
#        commands. Delete steps that do not apply; do not invent new tooling.
#     3. Prefer idempotent commands that can be re-run safely.
#     4. Anything that needs external credentials (hosting provider, S3, VPN)
#        should be described in a comment and confirmed with the user rather
#        than guessed.
#     5. Verify by running `wtx run refresh`, then `wtx add <branch>` and check
#        the new worktree picks up the published artifacts.
#
# ENVIRONMENT (set by wtx)
#   WTX_PROJECT_ROOT        Project root (where .worktree.yml lives)
#   WTX_SHARED_PATH         Shared directory; copy/ is copied into new
#                          worktrees, symlink/ is symlinked
#   WTX_MAIN_BRANCH         Main branch name from .worktree.yml
#   WTX_MAIN_WORKTREE_PATH  Path of the main branch's worktree, or empty if it
#                          is not checked out
#   WTX_WORKTREE_PATH       Worktree this was invoked from (empty at the root)
#   WTX_BRANCH_NAME         Branch of that worktree
#   WTX_WORKTREE_ID         Branch name sanitized for filesystem use
#   WTX_SCRIPT_NAME         "refresh"
#
# Legacy WT_* aliases are also exported during the transition. The resolution
# examples below prefer WTX_* and fall back to WT_* when unset or empty.
#
# The examples use docker compose because it is common; substitute the
# project's own tooling (ddev, lando, make, npm scripts, ...).

set -euo pipefail

usage() {
  echo "Usage: wtx run refresh"
  echo "Rebuilds the shared local environment from the main worktree."
}

for arg in "$@"; do
  case "$arg" in
    -h|--help) usage; exit 0 ;;
    *) echo "Error: unexpected argument: $arg" >&2; usage >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# 0. Resolve where to work.
#    Refreshes normally run against the main worktree, not the one you happen
#    to be in, so the captured state is always "main + latest data".
# ---------------------------------------------------------------------------
# main="${WTX_MAIN_WORKTREE_PATH:-${WT_MAIN_WORKTREE_PATH:-}}"
# shared="${WTX_SHARED_PATH:-${WT_SHARED_PATH:-}}"
# : "${main:?main worktree is not checked out; run: wtx add ${WTX_MAIN_BRANCH:-${WT_MAIN_BRANCH:-main}}}"
# : "${shared:?WTX_SHARED_PATH (or WT_SHARED_PATH) is not set; run this via: wtx run refresh}"
# cd "$main"
# echo "==> Refreshing from: $main"

# ---------------------------------------------------------------------------
# 1. Start the local services.
# ---------------------------------------------------------------------------
# echo "==> Starting services..."
# docker compose up -d --wait

# ---------------------------------------------------------------------------
# 2. Update the code on main.
# ---------------------------------------------------------------------------
# echo "==> Pulling latest..."
# git pull --ff-only

# ---------------------------------------------------------------------------
# 3. Refresh the data.
#    Import from a hosting provider, download a sanitized dump, run seeders,
#    or run the app's own sync command. Then apply pending migrations so the
#    captured state matches the code.
# ---------------------------------------------------------------------------
# echo "==> Importing data..."
# docker compose exec -T db sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" app' < ./seed.sql
# docker compose exec -T app ./bin/migrate

# ---------------------------------------------------------------------------
# 4. Capture the expensive state.
# ---------------------------------------------------------------------------
# echo "==> Dumping database..."
# tmp="$(mktemp -d)"
# docker compose exec -T db sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" app' | zstd > "$tmp/snapshot.sql.zst"

# ---------------------------------------------------------------------------
# 5. Publish into shared/ so new worktrees inherit it.
#    Files under $shared/copy are copied into every new worktree at the same
#    relative path; a matching setup hook in .worktree.yml can then restore
#    them (e.g. `setup: [./bin/restore-snapshot]`).
# ---------------------------------------------------------------------------
# echo "==> Publishing to shared..."
# mkdir -p "$shared/copy/db"
# mv -f "$tmp/snapshot.sql.zst" "$shared/copy/db/snapshot.sql.zst"
# rm -rf "$tmp"
# echo "==> Done: $shared/copy/db/snapshot.sql.zst"

# ---------------------------------------------------------------------------
# Not implemented yet. Delete this block once the steps above are filled in.
# ---------------------------------------------------------------------------
echo "refresh is not implemented for this project yet." >&2
echo "Edit ${BASH_SOURCE[0]} and follow the comments inside." >&2
exit 0
