# The Project root keeps its shared files; wtx apply never targets it

In a Checkout or In-repo layout the Project root is the Main worktree, the user's own checkout, and is not Managed, so `wtx apply` never writes into it. To give the root and every worktree one source of truth for a machine-local file such as `.env`, `wtx share` leaves the root's file where it is: `--symlink` links `shared/symlink/<path>` back to the root's file and every worktree links to that entry, and `--copy` adds the path to `.worktreeinclude`. In a Bare layout no worktree is special, so `wtx share` moves or copies the file from the current worktree into `shared/` and applies it to the others.

## Considered Options

- **Let `wtx apply` target the Project root.** Rejected: applying replaces whatever is at each destination (symlinks remove it, shared copies overwrite it), so it would destroy the user's real files; a `shared/symlink/` entry that points at the root would become a link to itself; and the root, the `.worktreeinclude` source, would also be an apply target, blurring which side is the source.
- **Move the root's file into `shared/symlink/` and link the root to it.** Rejected: it turns a real file in the user's checkout into a symlink into the Clone dir, under their editor and tools, and makes the checkout depend on wtx's machine-local state.
- **Convert a checkout to a Bare layout in place.** Rejected: it moves the user's working directory out from under their editor and tooling; `wtx clone` already serves users who want a Bare layout.
