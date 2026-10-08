# wtx

wtx manages a git repository as a set of worktrees, so several branches can be checked out and worked on side by side, each with the machine-local files it needs.

## Projects and clones

**Project**:
A repository managed by wtx, defined by its committed `.worktree.yml`. It is shared by everyone who works on the repository.
_Avoid_: repo (when the wtx-managed whole is meant), workspace

**Clone**:
One machine's copy of a Project, created with `wtx clone` or `wtx init`. Each Clone has exactly one Layout.
_Avoid_: checkout, copy, cloned project

**Clone name**:
The per-machine name that picks a Clone's Clone dir. It is never committed, so two Clones of the same Project can use different names.
_Avoid_: project name

**Project root**:
The directory of a Clone that holds `.worktree.yml`.
_Avoid_: repo root, main checkout

**Join**:
Setting up a Clone of a Project whose `.worktree.yml` already exists, without changing the committed configuration.
_Avoid_: re-init, adopt

## Layouts

**Layout**:
How a Clone's git data, worktrees, and shared files are arranged on disk.
_Avoid_: mode, style

**Bare layout**:
A Layout where the git data is a bare repository and everything else lives under the Project root.
_Avoid_: clone layout, cloned project

**Checkout layout**:
A Layout built on an ordinary checkout, whose machine-local state lives in the Clone dir.
_Avoid_: init project, non-bare, home layout

**In-repo layout**:
A Checkout layout that keeps its worktrees, shared files, and scripts inside the Project root instead of a Clone dir.
_Avoid_: legacy layout, nested worktrees

## Machine-local state

**wtx home**:
The per-user directory that holds every Clone dir on a machine, `~/.wtx` by default.
_Avoid_: home, home dir

**Clone dir**:
The directory inside the wtx home that holds one Clone's worktrees, shared files, and scripts.
_Avoid_: project home, home project dir, clone home

**Owner marker**:
The record in a Clone dir naming the Project root it belongs to.
_Avoid_: marker file, project file

**Orphaned Clone dir**:
A Clone dir whose Owner marker names a Project root that no longer exists or is no longer a Project.
_Avoid_: stale home, leftover dir

## Worktrees

**Main worktree**:
The worktree on the Project's main branch. In a Checkout layout it is the Project root itself.
_Avoid_: main checkout, primary worktree

**Managed worktree**:
Any linked worktree of a Clone, wherever it lives on disk. In a Bare layout this includes the Main worktree; in a Checkout layout the Main worktree is the Project root and is not managed.
_Avoid_: wtx worktree

**External worktree**:
A Managed worktree that lives outside the Clone's worktrees directory, typically created by another tool.
_Avoid_: foreign worktree, unmanaged worktree

## Files carried into worktrees

**Shared files**:
Machine-local files that every new worktree receives from the Clone, either as copies or as symlinks.
_Avoid_: templates (except for files rendered from a template), common files

**Include file**:
The committed `.worktreeinclude` listing which ignored files from the Main worktree each new worktree should get.
_Avoid_: include list, worktree include

**Seeding**:
Copying a file into a worktree only when the worktree does not already have it.
_Avoid_: syncing, applying (when no overwrite is meant)

**Migration**:
Moving a Clone from the In-repo layout to a Checkout layout that uses a Clone dir.
_Avoid_: relocation, move-home
