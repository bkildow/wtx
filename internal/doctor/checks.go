package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/disk"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

func (s *inspection) scripts() {
	if len(s.cfg.Scripts) == 0 {
		// Scripts are optional; their absence is not a health problem.
		s.add("scripts", "ok", filepath.Join(s.root, config.ConfigFileName), "No project scripts are configured.", "")
		return
	}
	for _, name := range project.ScriptNames(s.cfg) {
		path := s.cfg.Scripts[name]
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		if _, err := project.ResolveScript(s.cfg, s.root, name); err != nil {
			s.problem("scripts", path, err)
		} else {
			s.add("scripts", "ok", path, "Script "+name+" is executable.", "")
		}
	}
}

func (s *inspection) disk() {
	disable := config.LookupEnv("NO_DISK_WARN")
	threshold := s.cfg.DiskThreshold()
	if disable != "" || threshold == nil {
		s.add("disk", "ok", s.root, "Disk warnings disabled by configuration.", "")
		return
	}
	u, err := disk.Stat(s.root)
	if err != nil {
		s.problem("disk", s.root, err)
		return
	}
	severity, remedy := OK, ""
	if threshold.IsLow(u) {
		severity = "warn"
		remedy = "Review disk usage and unused worktrees with wtx list before choosing cleanup actions."
	}
	s.add("disk", severity, s.root, fmt.Sprintf("%.1f%% free disk space (%d bytes).", u.PercentFree(), u.FreeBytes), remedy)
}

func (s *inspection) teardown(root string) {
	if len(s.cfg.Teardown) > 0 || len(s.cfg.ParallelTeardown) > 0 {
		return
	}
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			s.problem("teardown", path, err)
			continue
		}
		if !info.IsDir() {
			s.add("teardown", "warn", path, "Compose configuration exists but teardown and parallel_teardown are empty.", "Review resource cleanup and configure appropriate teardown hooks; removing worktrees does not clean up external resources.")
		}
	}
}

func (s *inspection) gitVersion(ctx context.Context) {
	v, err := s.runner.Version(ctx)
	if err != nil {
		s.problem("git.version", "", err)
		return
	}
	severity, remedy := OK, ""
	if v[0] < 2 || (v[0] == 2 && v[1] < 20) {
		severity = "fail"
		remedy = "Upgrade Git to 2.20 or newer (2.48+ recommended)."
	} else if v[0] == 2 && v[1] < 48 {
		severity = "warn"
		remedy = "Upgrade Git to 2.48+ for relative-path worktrees."
	}
	s.add("git.version", severity, "", fmt.Sprintf("Git %d.%d.%d", v[0], v[1], v[2]), remedy)
}

func (s *inspection) exclusions() {
	path := filepath.Join(s.runner.GitDir, "info", "exclude")
	file, err := takeSnapshot(path)
	if err != nil {
		s.problem("git.exclude", path, err)
		return
	}
	updated := project.ManagedExclude(file.data)
	if bytes.Equal(file.data, updated) {
		s.add("git.exclude", "ok", path, "Managed exclusions are current.", "")
		return
	}
	i := s.add("git.exclude", "warn", path, "Managed exclusions have a legacy marker or missing setup-state/log patterns.", "Run wtx doctor --fix to update managed exclusions.")
	s.planContent(i, file, updated, nil)
}

func (s *inspection) compatibility(ctx context.Context, worktrees []git.WorktreeInfo) {
	common := filepath.Join(s.runner.GitDir, "config")
	bare, err := git.ConfigBool(ctx, common, "core.bare")
	if err != nil {
		s.problem("git.compatibility", common, err)
		return
	}
	if bare != "true" {
		s.add("git.compatibility", "ok", common, "Repository does not require bare-worktree overrides.", "")
		return
	}
	s.configRepair(ctx, common, "extensions.worktreeConfig", "true")
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
			continue
		}
		path, err := git.WorktreeConfigPath(wt.Path)
		if err != nil {
			s.problem("git.compatibility", wt.Path, err)
			continue
		}
		// Guard the pointer as well as the administrative configuration file.
		pointer, err := takeSnapshot(filepath.Join(wt.Path, ".git"))
		if err != nil {
			s.problem("git.compatibility", wt.Path, err)
			continue
		}
		s.configRepair(ctx, path, "core.bare", "false", pointer)
	}
}

func (s *inspection) configRepair(ctx context.Context, path, key, want string, guards ...snapshot) {
	file, err := takeSnapshot(path)
	if err != nil {
		s.problem("git.compatibility", path, err)
		return
	}
	value := ""
	if file.info != nil {
		value, err = git.ConfigBool(ctx, path, key)
	}
	if err != nil {
		s.problem("git.compatibility", path, err)
		return
	}
	if value == want {
		s.add("git.compatibility", "ok", path, key+"="+want+" is configured.", "")
		return
	}
	i := s.add("git.compatibility", "fail", path, "Missing required "+key+"="+want+" for bare repository worktrees.", "Run wtx doctor --fix (or wtx repair) to repair Git compatibility.")
	s.planConfig(i, file, key, want, guards...)
}

func (s *inspection) branches(ctx context.Context, worktrees []git.WorktreeInfo) {
	output, err := s.runner.Query(ctx, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		s.problem("git.branches", s.runner.GitDir, err)
		return
	}
	used := map[string]bool{s.cfg.MainBranchOrDefault(): true}
	for _, wt := range worktrees {
		used[wt.Branch] = true
	}
	refs := strings.Split(output, "\n")
	for _, ref := range refs {
		if remote, ok := strings.CutPrefix(ref, "refs/remotes/"); ok {
			_, branch, _ := strings.Cut(remote, "/")
			used[branch] = true
		}
	}
	for _, ref := range refs {
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok && !used[branch] {
			s.add("git.branches", "warn", s.runner.GitDir, "Local branch "+branch+" has no worktree or matching remote-tracking branch.", "Review the branch locally; doctor never deletes branches or fetches remotes.")
		}
	}
}

func (s *inspection) states(root string) {
	for _, name := range []string{project.SetupStateFile, project.LegacySetupStateFile} {
		path := filepath.Join(root, name)
		file, err := takeSnapshot(path)
		if err != nil {
			s.problem("setup.state", path, err)
			continue
		}
		if file.info == nil {
			continue
		}
		var state project.SetupState
		if err := json.Unmarshal(file.data, &state); err != nil {
			s.problem("setup.state", path, err)
			continue
		}
		switch state.Status {
		case project.SetupRunning, project.SetupComplete, project.SetupFailed, project.SetupSkipped:
		default:
			s.problem("setup.state", path, fmt.Errorf("invalid setup status"))
			continue
		}
		if state.HooksCompleted < 0 || state.HooksTotal < 0 || state.HooksCompleted > state.HooksTotal || (state.Status == project.SetupRunning && state.PID <= 0) {
			s.problem("setup.state", path, fmt.Errorf("invalid setup counters or process ID"))
			continue
		}
		pid := state.PID
		switch {
		case project.ResolveDeadSetupProcess(&state):
			i := s.add("setup.state", "fail", path, "Setup is marked running but its process has exited.", "Run wtx doctor --fix to reconcile state; review the retained log before rerunning wtx setup.")
			data, err := project.EncodeSetupState(&state)
			if err != nil {
				s.problem("setup.state", path, err)
				continue
			}
			s.planContent(i, file, data, func() error {
				if project.IsProcessAlive(pid) {
					return fmt.Errorf("setup process is now alive; refusing repair")
				}
				return nil
			})
		case state.Status == project.SetupFailed:
			s.add("setup.state", "fail", path, "Setup failed; logs have been retained.", "Review the setup log and rerun wtx setup when ready.")
		default:
			s.add("setup.state", "ok", path, "Setup status: "+string(state.Status)+".", "")
		}
	}
}

func (s *inspection) shared(root string) {
	shared := project.SharedPath(s.root, s.cfg)
	copyDir := filepath.Join(shared, "copy")
	err := filepath.WalkDir(copyDir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == copyDir {
			return nil
		}
		if err != nil {
			s.problem("shared.copy", path, err)
			return nil
		}
		if repairArtifact(entry.Name()) {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(copyDir, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(root, project.StripTemplateExt(rel))
		info, err := os.Stat(dest)
		if os.IsNotExist(err) {
			s.add("shared.copy", "warn", dest, "Expected shared copy is missing.", "Review and run wtx apply for this worktree (or wtx apply --all).")
			return nil
		}
		if err != nil {
			s.problem("shared.copy", dest, err)
		} else if info.IsDir() {
			s.add("shared.copy", "fail", dest, "Expected shared file is a directory.", "Review the destination before running wtx apply.")
		}
		return nil
	})
	if err != nil {
		s.problem("shared.copy", copyDir, err)
	}
	s.links(filepath.Join(shared, "symlink"), root)
	s.danglingManagedLinks(root, filepath.Join(shared, "symlink"))
}

// Inspect destinations too: a deleted shared source is no longer enumerable,
// but links pointing into the managed tree still need a finding.
func (s *inspection) danglingManagedLinks(root, shared string) {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			s.problem("shared.symlink", path, err)
			return nil
		}
		if entry.IsDir() {
			if path != root && skippedName(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			s.problem("shared.symlink", path, err)
			return nil
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if !within(resolved(shared), resolved(target)) {
			return nil
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			s.problem("shared.symlink", path, err)
			return nil
		}
		if s.seenLinks[path] {
			return nil
		}
		s.add("shared.symlink", "warn", path, "Managed symlink points to a shared source that no longer exists.", "Restore the shared source or review and repair the link manually.")
		return nil
	})
	if err != nil {
		s.problem("shared.symlink", root, err)
	}
}

func (s *inspection) links(source, dest string) {
	entries, err := os.ReadDir(source)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.problem("shared.symlink", source, err)
		return
	}
	for _, entry := range entries {
		if repairArtifact(entry.Name()) {
			continue
		}
		src, target := filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name())
		info, err := os.Lstat(target)
		if err == nil && entry.IsDir() && info.IsDir() {
			s.links(src, target)
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			s.problem("shared.symlink", target, err)
			continue
		}
		actual, linkErr := filepath.EvalSymlinks(target)
		expected, srcErr := filepath.EvalSymlinks(src)
		if err != nil || info.Mode()&os.ModeSymlink == 0 || linkErr != nil || srcErr != nil || actual != expected {
			s.seenLinks[target] = true
			s.add("shared.symlink", "warn", target, "Managed symlink is missing, broken, or points to an unexpected target.", "Review the link and shared source, then repair manually or run wtx apply for this worktree.")
		}
	}
}
