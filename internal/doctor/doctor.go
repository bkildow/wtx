// Package doctor inspects project health and applies only explicitly planned,
// guarded repairs. Inspection never changes the project.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

type Finding struct {
	ID          string `json:"check_id"`
	Severity    string `json:"severity"`
	Path        string `json:"path,omitempty"`
	Line        int    `json:"line,omitempty"`
	Explanation string `json:"explanation"`
	Remedy      string `json:"remedy,omitempty"`
	Repairable  bool   `json:"repairable"`
}

type RepairOutcome struct {
	ID     string `json:"check_id"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Backup string `json:"backup,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Counts struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type Report struct {
	SchemaVersion int             `json:"schema_version"`
	Scope         string          `json:"scope"`
	Findings      []Finding       `json:"findings"`
	Repairs       []RepairOutcome `json:"repairs"`
	Counts        Counts          `json:"counts"`
}

func (r Report) Unsuccessful(strict bool) bool {
	if r.Counts.Fail > 0 || (strict && r.Counts.Warn > 0) {
		return true
	}
	for _, repair := range r.Repairs {
		if repair.Status == "failed" {
			return true
		}
	}
	return false
}

type Options struct {
	StartDir string
	User     bool
	Fix      bool
	DryRun   bool
}

type inspection struct {
	report       Report
	root         string
	cfg          *config.Config
	runner       *git.Runner
	repairs      []repair
	guards       []snapshot
	seenSettings map[string]bool
	seenScan     map[string]bool
	seenLinks    map[string]bool
	gitDir       string // resolved Git directory, excluded from scans
	scanSkipped  int
}

// Run always returns a report, including discovery and operational failures.
func Run(ctx context.Context, opts Options) Report {
	s := inspect(ctx, opts)
	if opts.Fix && !opts.User {
		outcomes := make([]RepairOutcome, 0, len(s.repairs))
		for _, change := range s.repairs {
			outcome := RepairOutcome{ID: change.id, Path: change.file.path, Status: "planned"}
			if !opts.DryRun {
				var err error
				outcome.Backup, err = change.apply(ctx)
				outcome.Status = "applied"
				if err != nil {
					outcome.Status = "failed"
					outcome.Error = err.Error()
				}
			}
			outcomes = append(outcomes, outcome)
		}
		if !opts.DryRun {
			changes := s.repairs
			s = inspect(ctx, opts)
			for i := range outcomes {
				if outcomes[i].Status != "applied" {
					continue
				}
				if err := changes[i].verify(ctx); err != nil {
					outcomes[i].Status = "failed"
					outcomes[i].Error = "repair verification: " + err.Error()
					continue
				}
				for _, f := range s.report.Findings {
					if f.ID == outcomes[i].ID && f.Path == outcomes[i].Path && f.Repairable {
						outcomes[i].Status = "failed"
						outcomes[i].Error = "repair did not pass verification"
					}
				}
			}
		}
		s.report.Repairs = outcomes
	}
	s.report.finish()
	return s.report
}

func (r *Report) finish() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Explanation < b.Explanation
	})
	sort.SliceStable(r.Repairs, func(i, j int) bool {
		if r.Repairs[i].ID != r.Repairs[j].ID {
			return r.Repairs[i].ID < r.Repairs[j].ID
		}
		return r.Repairs[i].Path < r.Repairs[j].Path
	})
	r.Counts = Counts{}
	for _, f := range r.Findings {
		switch f.Severity {
		case "ok":
			r.Counts.OK++
		case "warn":
			r.Counts.Warn++
		case "fail":
			r.Counts.Fail++
		}
	}
}

func (s *inspection) add(id, severity, path, explanation, remedy string) int {
	s.report.Findings = append(s.report.Findings, Finding{ID: id, Severity: severity, Path: path, Explanation: explanation, Remedy: remedy})
	return len(s.report.Findings) - 1
}

func (s *inspection) problem(id, path string, err error) {
	s.add(id, "fail", path, "Inspection failed: "+err.Error(), "Correct the file or access permissions and rerun wtx doctor.")
}

func (s *inspection) blocked(ids ...string) {
	for _, id := range ids {
		s.add(id, "fail", "", "Check blocked by unavailable project configuration or Git worktree information.", "Resolve the prerequisite findings and rerun wtx doctor.")
	}
}

func inspect(ctx context.Context, opts Options) *inspection {
	s := &inspection{report: Report{SchemaVersion: 1, Scope: "project", Findings: []Finding{}, Repairs: []RepairOutcome{}}, seenSettings: map[string]bool{}, seenScan: map[string]bool{}, seenLinks: map[string]bool{}}
	if opts.User {
		s.report.Scope = "user"
		if opts.Fix {
			s.add("user.repair", "fail", "", "--user --fix is not supported: dotfile changes are manual.", "Run wtx doctor --user and review the suggested replacements.")
		}
		s.user()
		return s
	}
	start := opts.StartDir
	var err error
	if start == "" {
		start, err = os.Getwd()
	}
	if err == nil {
		start, err = filepath.Abs(start)
	}
	if err == nil {
		s.root, err = project.FindRoot(start)
	}
	if err == nil {
		s.root, err = filepath.EvalSymlinks(s.root)
	}
	if err != nil {
		s.problem("project.config", start, err)
		s.blocked("git.compatibility", "git.worktrees", "shared.copy", "shared.symlink", "setup.state", "scripts", "teardown", "disk", "migration.references", "claude.hooks", "git.branches")
		return s
	}
	configPath := filepath.Join(s.root, config.ConfigFileName)
	guard, err := takeSnapshot(configPath)
	if err != nil {
		s.problem("project.config", configPath, err)
		s.blocked("git.compatibility", "git.worktrees", "shared.copy", "shared.symlink", "setup.state", "scripts", "teardown", "disk", "migration.references", "claude.hooks", "git.branches", "git.exclude")
		return s
	}
	s.cfg, err = config.Load(s.root)
	if err == nil {
		err = guard.unchanged()
	}
	if err != nil {
		s.problem("project.config", configPath, err)
		s.blocked("git.compatibility", "git.worktrees", "shared.copy", "shared.symlink", "setup.state", "scripts", "teardown", "disk", "migration.references", "claude.hooks", "git.branches", "git.exclude")
		return s
	}
	s.guards = append(s.guards, guard)
	s.gitDir = resolved(project.GitDirPath(s.root, s.cfg))
	defer s.scanLimitations(s.root)
	s.add("project.config", "ok", configPath, "Project configuration is readable.", "")
	s.scripts()
	s.disk()
	s.teardown(s.root)
	s.settings(filepath.Join(s.root, ".claude", "settings.local.json"))
	s.settings(filepath.Join(s.root, ".claude", "settings.json"))
	s.scanProject()
	gitDir := project.GitDirPath(s.root, s.cfg)
	s.runner = git.NewRunner(gitDir, false)
	s.runner.Quiet = true
	s.gitVersion(ctx)
	if _, err := s.runner.Query(ctx, "rev-parse", "--git-dir"); err != nil {
		s.problem("project.git", gitDir, err)
		s.blocked("git.compatibility", "git.worktrees", "git.branches", "shared.copy", "shared.symlink", "setup.state", "git.exclude")
		return s
	}
	s.add("project.git", "ok", gitDir, "Git directory is usable.", "")
	s.exclusions()
	worktrees, err := s.runner.WorktreeList(ctx)
	if err != nil {
		s.problem("git.worktrees", gitDir, err)
		s.blocked("git.compatibility", "git.branches", "shared.copy", "shared.symlink", "setup.state")
		return s
	}
	s.compatibility(ctx, worktrees)
	s.branches(ctx, worktrees)
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		info, err := os.Stat(wt.Path)
		if os.IsNotExist(err) {
			message, remedy := "Worktree directory is missing.", "Restore or review the missing worktree registration."
			if wt.Prunable {
				message += " Git marks it prunable."
				remedy = fmt.Sprintf("Review git --git-dir=%q worktree prune --dry-run, then git --git-dir=%q worktree prune.", gitDir, gitDir)
			}
			if wt.Locked {
				message += " Registration is locked."
				remedy = "Restore the directory or review its lock before taking manual action."
			}
			s.add("git.worktrees", "warn", wt.Path, message, remedy)
			continue
		}
		if err != nil {
			s.problem("git.worktrees", wt.Path, err)
			continue
		}
		if !info.IsDir() {
			s.problem("git.worktrees", wt.Path, fmt.Errorf("worktree path is not a directory"))
			continue
		}
		s.add("git.worktrees", "ok", wt.Path, "Worktree directory exists.", "")
		s.states(wt.Path)
		if resolved(wt.Path) != s.root {
			s.shared(wt.Path)
			s.teardown(wt.Path)
		}
		for _, name := range []string{"settings.local.json", "settings.json"} {
			s.settings(filepath.Join(wt.Path, ".claude", name))
		}
		s.scanTracked(ctx, wt.Path)
	}
	return s
}

func resolved(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return filepath.Clean(path)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
