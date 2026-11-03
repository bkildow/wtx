package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

const (
	shareSymlink = "symlink"
	shareCopy    = "copy"
)

func newShareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "share [path]",
		Short: "Share a machine-local file with every worktree",
		Long: `Share a machine-local file or directory with every worktree.

In a checkout, the file stays in the project root:
  --symlink  links shared/symlink/<path> to the root's file, then links every
             worktree to it, so all of them use the root's file
  --copy     adds the path to .worktreeinclude (commit it), then copies it into
             existing worktrees that lack it

In a bare layout, run it from the worktree that has the file:
  --symlink  moves the file into shared/symlink/ and links every worktree to it
  --copy     copies the file into shared/copy/ and into worktrees that lack it

Without a path, pick from the untracked files; without --symlink or --copy,
pick the mode. Worktrees that already have their own file at the path are
left alone and listed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runShare,
	}
	cmd.Flags().Bool(shareSymlink, false, "Link the file into every worktree")
	cmd.Flags().Bool(shareCopy, false, "Copy the file into every worktree")
	cmd.MarkFlagsMutuallyExclusive(shareSymlink, shareCopy)
	return cmd
}

func runShare(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	if err := clone.CheckOwned(); err != nil {
		return err
	}
	wts, err := clone.Worktrees(ctx)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	bare := clone.Layout() == project.BareLayout
	source, err := shareSource(bare, clone.Root(), wts, cwd)
	if err != nil {
		return err
	}
	canonicalSource := ui.CanonicalPath(source)
	managed := managedWithin(canonicalSource, clone.ManagedDirs())

	var rel string
	if len(args) > 0 {
		rel, err = resolveSharePath(canonicalSource, cwd, args[0], managed)
	} else {
		rel, err = pickSharePath(ctx, canonicalSource, managed)
	}
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}
	if err := project.CheckShareSource(source, rel); err != nil {
		return err
	}
	if tracked, err := git.IsTracked(ctx, source, rel); err != nil {
		return err
	} else if tracked {
		return fmt.Errorf("%s is tracked by git; shared files are machine-local", rel)
	}

	mode, err := shareMode(cmd, bare, rel)
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}
	if err := sharedElsewhere(clone.SharedDir(), rel, mode); err != nil {
		return err
	}

	// Put the file in place, then give it to every Managed worktree but the
	// source and those whose directory is gone (prunable).
	symlinkDir := filepath.Join(clone.SharedDir(), "symlink")
	switch {
	case mode == shareSymlink && bare:
		if err := project.MoveToShared(symlinkDir, source, rel, dry); err != nil {
			return err
		}
	case mode == shareSymlink:
		already, err := project.LinkBack(symlinkDir, source, rel, dry)
		if err != nil {
			return err
		}
		if already {
			ui.Info(fmt.Sprintf("  %s is already shared from the project root", rel))
		}
	case bare:
		if err := project.CopyToShared(filepath.Join(clone.SharedDir(), "copy"), source, rel, dry); err != nil {
			return err
		}
	default:
		if err := addWorktreeInclude(ctx, source, rel, dry); err != nil {
			return err
		}
	}

	targets := slices.DeleteFunc(slices.Clone(wts.Managed), func(wt git.WorktreeInfo) bool {
		return wt.Prunable || project.SamePath(wt.Path, source)
	})
	var outcomes []project.ShareOutcome
	if mode == shareSymlink {
		outcomes, err = project.LinkInto(symlinkDir, rel, targets, dry)
	} else {
		outcomes, err = project.SeedInto(source, rel, targets, dry)
	}
	if err != nil {
		return err
	}

	// wtx apply replaces a worktree's own file, except for .worktreeinclude
	// copies, which only seed.
	reportShare(rel, mode, outcomes, mode == shareSymlink || bare)
	return nil
}

// shareSource is the directory a file is shared from: the Project root in a
// Checkout or In-repo layout, the Managed worktree holding cwd in a Bare
// layout.
func shareSource(bare bool, root string, wts project.Worktrees, cwd string) (string, error) {
	if !bare {
		return root, nil
	}
	wt, ok := worktreeContaining(wts.Managed, cwd)
	if !ok {
		return "", errors.New("run wtx share from inside the worktree that has the file\n  in a bare layout it shares a file from the current worktree")
	}
	return wt.Path, nil
}

// managedWithin returns the canonical spellings of managedDirs that lie
// strictly inside source (canonical), such as an In-repo layout's
// .worktrees. A managed directory holding source, such as a Bare layout's
// worktrees directory, is left out.
func managedWithin(source string, managedDirs []string) []string {
	var within []string
	for _, dir := range managedDirs {
		if rel, ok := ui.RelWithin(source, ui.CanonicalPath(dir)); ok && rel != "." {
			within = append(within, filepath.Join(source, rel))
		}
	}
	return within
}

// resolveSharePath returns arg relative to source (canonical). A relative
// arg is read against cwd when cwd lies inside source and outside managed
// (see managedWithin), else against source. The path must lie inside source
// and must not be wtx's or git's own.
func resolveSharePath(source, cwd, arg string, managed []string) (string, error) {
	base := source
	canonicalCwd := ui.CanonicalPath(cwd)
	if ui.Within(source, canonicalCwd) && !slices.ContainsFunc(managed, func(dir string) bool {
		return ui.Within(dir, canonicalCwd)
	}) {
		base = canonicalCwd
	}
	abs := arg
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(base, arg)
	}
	// Resolve the parent only: the path itself may be a symlink, which
	// CheckShareSource reports.
	abs = filepath.Join(ui.CanonicalPath(filepath.Dir(abs)), filepath.Base(abs))
	rel, ok := ui.RelWithin(source, abs)
	if !ok || rel == "." {
		return "", fmt.Errorf("%s is not a path inside %s", arg, source)
	}
	if reason := reservedSharePath(source, rel, managed); reason != "" {
		return "", fmt.Errorf("cannot share %s: %s", rel, reason)
	}
	return rel, nil
}

// reservedSharePath says why rel, below source, cannot be shared, or
// returns "". managed is as returned by managedWithin.
func reservedSharePath(source, rel string, managed []string) string {
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	switch {
	case first == ".git":
		return "it is git's own data"
	case rel == config.ConfigFileName:
		return "it is the project's configuration"
	}
	abs := filepath.Join(source, rel)
	for _, dir := range managed {
		if ui.Within(abs, dir) || ui.Within(dir, abs) {
			return "it holds wtx's worktrees, shared files or scripts"
		}
	}
	return ""
}

// pickSharePath asks for one of the untracked top-level entries of source
// (canonical).
func pickSharePath(ctx context.Context, source string, managed []string) (string, error) {
	untracked, err := git.ListUntracked(ctx, source)
	if err != nil {
		return "", err
	}
	candidates := shareCandidates(source, untracked, managed)
	if len(candidates) == 0 {
		return "", fmt.Errorf("no untracked files to share in %s; pass a path", source)
	}
	return (&ui.InteractivePrompter{}).SelectPath("Select a file to share", candidates)
}

// shareCandidates keeps the top-level entries of untracked (as listed by
// git.ListUntracked) that can be shared: not symlinks and not wtx's own
// directories.
func shareCandidates(source string, untracked, managed []string) []string {
	var out []string
	for _, p := range untracked {
		name := strings.TrimSuffix(p, "/")
		if !strings.Contains(name, "/") && project.CheckShareSource(source, name) == nil && reservedSharePath(source, name, managed) == "" {
			out = append(out, name)
		}
	}
	return out
}

// shareMode returns the --symlink or --copy mode, or asks for one.
func shareMode(cmd *cobra.Command, bare bool, rel string) (string, error) {
	for _, mode := range []string{shareSymlink, shareCopy} {
		if on, _ := cmd.Flags().GetBool(mode); on {
			return mode, nil
		}
	}
	symlinkHint, copyHint := "every worktree links to the root's "+rel, "added to .worktreeinclude; worktrees get a copy"
	if bare {
		symlinkHint, copyHint = "moved into shared/symlink; every worktree links to it", "copied into shared/copy; every worktree gets a copy"
	}
	return (&ui.InteractivePrompter{}).SelectOption("Share "+rel+" as", [][2]string{
		{shareSymlink + ": " + symlinkHint, shareSymlink},
		{shareCopy + ": " + copyHint, shareCopy},
	})
}

// addWorktreeInclude adds rel to the root's .worktreeinclude unless it
// already selects it. .worktreeinclude only copies ignored files, so rel
// must be ignored.
func addWorktreeInclude(ctx context.Context, root, rel string, dryRun bool) error {
	ignored, err := git.IsIgnored(ctx, root, rel)
	if err != nil {
		return err
	}
	if !ignored {
		return fmt.Errorf("%s is not ignored by git; .worktreeinclude only copies ignored files\n  add it to .gitignore, or share it with --symlink", rel)
	}
	if included, err := git.WorktreeIncludeSelects(ctx, root, rel); err != nil {
		return err
	} else if included {
		ui.Info(fmt.Sprintf("  %s already selects %s", git.WorktreeIncludeFile, rel))
		return nil
	}
	pattern := "/" + filepath.ToSlash(rel)
	if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
		pattern += "/"
	}
	if err := project.AppendWorktreeInclude(root, pattern, dryRun); err != nil {
		return err
	}
	ui.Info(fmt.Sprintf("  added %s to %s; commit it so other clones get it too", pattern, git.WorktreeIncludeFile))
	return nil
}

// sharedElsewhere refuses to share rel in mode when the other mode's
// shared directory already holds it: shared/symlink wins over a copy, and a
// link would make an existing shared/copy entry dead.
func sharedElsewhere(sharedDir, rel, mode string) error {
	other := shareCopy
	if mode == shareCopy {
		other = shareSymlink
	}
	path := filepath.Join(sharedDir, other, rel)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s is already shared with --%s (%s)", rel, other, path)
	}
	return nil
}

// reportShare lists the worktrees that already had rel and prints a
// summary. applyHint suggests wtx apply for a worktree that kept its own.
func reportShare(rel, mode string, outcomes []project.ShareOutcome, applyHint bool) {
	verb := "linked"
	if mode == shareCopy {
		verb = "copied"
	}
	var done int
	for _, o := range outcomes {
		// A detached worktree has no branch to name it by.
		name := cmp.Or(o.Worktree.Branch, o.Worktree.Path)
		switch o.State {
		case project.ShareDone:
			done++
		case project.ShareAlready:
			ui.Info(fmt.Sprintf("  %s already links to the shared %s", name, rel))
		case project.ShareSkipped:
			msg := fmt.Sprintf("  %s has its own %s; left it alone", name, rel)
			if applyHint && o.Worktree.Branch != "" {
				msg += fmt.Sprintf(" (remove it, then run 'wtx apply %s')", o.Worktree.Branch)
			}
			ui.Warning(msg)
		}
	}
	ui.Success(fmt.Sprintf("Shared %s (%s into %d worktree(s))", rel, verb, done))
}
