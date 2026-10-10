package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/disk"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

func (s *inspection) scripts() {
	cfg, root := s.clone.Config(), s.clone.Root()
	if len(cfg.Scripts) == 0 {
		// Scripts are optional; their absence is not a health problem.
		s.add("scripts", "ok", filepath.Join(root, config.ConfigFileName), "No project scripts are configured.", "")
		return
	}
	for _, name := range project.ScriptNames(cfg) {
		path := project.ExpandOrJoin(root, cfg.Scripts[name])
		if _, err := project.ResolveScript(cfg, root, name); err != nil {
			s.problem("scripts", path, err)
		} else {
			s.add("scripts", "ok", path, "Script "+name+" is executable.", "")
		}
	}
}

func (s *inspection) disk() {
	root := s.clone.Root()
	disable := config.LookupEnv("NO_DISK_WARN")
	threshold := s.clone.Config().DiskThreshold()
	if disable != "" || threshold == nil {
		s.add("disk", "ok", root, "Disk warnings disabled by configuration.", "")
		return
	}
	u, err := disk.Stat(root)
	if err != nil {
		s.problem("disk", root, err)
		return
	}
	severity, remedy := OK, ""
	if threshold.IsLow(u) {
		severity = "warn"
		remedy = "Review disk usage and unused worktrees with wtx list before choosing cleanup actions."
	}
	s.add("disk", severity, root, fmt.Sprintf("%.1f%% free disk space (%d bytes).", u.PercentFree(), u.FreeBytes), remedy)
}

func (s *inspection) teardown(root string) {
	if len(s.clone.Config().Teardown) > 0 || len(s.clone.Config().ParallelTeardown) > 0 {
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
	v, err := s.clone.Runner().Version(ctx)
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
	path := filepath.Join(s.clone.Runner().GitDir, "info", "exclude")
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
	common := filepath.Join(s.clone.Runner().GitDir, "config")
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
		// Prunable worktrees have no .git link to configure; git.worktrees reports them.
		if wt.Bare || wt.Prunable {
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
	runner := s.clone.Runner()
	output, err := runner.Query(ctx, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/")
	if err != nil {
		s.problem("git.branches", runner.GitDir, err)
		return
	}
	used := map[string]bool{s.clone.Config().MainBranchOrDefault(): true}
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
			s.addSubject("git.branches", runner.GitDir, branch, "Local branch "+branch+" has no worktree or matching remote-tracking branch.", "Review the branch locally; doctor never deletes branches or fetches remotes.")
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

func (s *inspection) shared(root string, f *worktreeFacts) {
	shared := s.clone.SharedDir()
	s.report.Findings = append(s.report.Findings, f.copies...)
	s.links(filepath.Join(shared, "symlink"), root)
	s.report.Findings = append(s.report.Findings, f.danglingErrs...)
	for _, path := range f.dangling {
		if !s.seenLinks[path] {
			s.add("shared.symlink", "warn", path, "Managed symlink points to a shared source that no longer exists.", "Restore the shared source or review and repair the link manually.")
		}
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
