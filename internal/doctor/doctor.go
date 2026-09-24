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

type Severity string

const (
	OK   Severity = "ok"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

type RepairStatus string

const (
	Planned RepairStatus = "planned"
	Applied RepairStatus = "applied"
	Failed  RepairStatus = "failed"
)

type Finding struct {
	ID          string   `json:"check_id"`
	Severity    Severity `json:"severity"`
	Path        string   `json:"path,omitempty"`
	Line        int      `json:"line,omitempty"`
	Explanation string   `json:"explanation"`
	Remedy      string   `json:"remedy,omitempty"`
	Repairable  bool     `json:"repairable"`
}

type RepairOutcome struct {
	ID     string       `json:"check_id"`
	Path   string       `json:"path"`
	Status RepairStatus `json:"status"`
	Backup string       `json:"backup,omitempty"`
	Error  string       `json:"error,omitempty"`
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
		if repair.Status == Failed {
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
	backups      string // repair backup directory
	resolvedDirs map[string]string
	scanSkipped  int
}

// Run always returns a report, including discovery and operational failures.
func Run(ctx context.Context, opts Options) Report {
	s := inspect(ctx, opts)
	if !opts.Fix || opts.User {
		s.report.finish()
		return s.report
	}
	repairs := s.repairs
	outcomes := make([]RepairOutcome, len(repairs))
	for i, change := range repairs {
		outcomes[i] = RepairOutcome{ID: change.id, Path: change.file.path, Status: Planned}
	}
	if !opts.DryRun {
		for i, change := range repairs {
			outcomes[i].Status = Applied
			var err error
			if outcomes[i].Backup, err = change.apply(ctx); err != nil {
				outcomes[i].Status, outcomes[i].Error = Failed, err.Error()
			}
		}
		s = inspect(ctx, opts)
		s.recheck(ctx, repairs, outcomes)
	}
	s.report.Repairs = outcomes
	s.report.finish()
	return s.report
}

// recheck fails applied repairs whose result differs from the plan or that
// the fresh inspection still considers repairable.
func (s *inspection) recheck(ctx context.Context, repairs []repair, outcomes []RepairOutcome) {
	pending := map[[2]string]bool{}
	for _, f := range s.report.Findings {
		if f.Repairable {
			pending[[2]string{f.ID, f.Path}] = true
		}
	}
	for i := range outcomes {
		if outcomes[i].Status != Applied {
			continue
		}
		if err := repairs[i].verify(ctx); err != nil {
			outcomes[i].Status, outcomes[i].Error = Failed, "repair verification: "+err.Error()
		} else if pending[[2]string{outcomes[i].ID, outcomes[i].Path}] {
			outcomes[i].Status, outcomes[i].Error = Failed, "repair did not pass verification"
		}
	}
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
		case OK:
			r.Counts.OK++
		case Warn:
			r.Counts.Warn++
		case Fail:
			r.Counts.Fail++
		}
	}
}

func (s *inspection) add(id string, severity Severity, path, explanation, remedy string) int {
	s.report.Findings = append(s.report.Findings, Finding{ID: id, Severity: severity, Path: path, Explanation: explanation, Remedy: remedy})
	return len(s.report.Findings) - 1
}

func (s *inspection) problem(id, path string, err error) {
	s.add(id, "fail", path, "Inspection failed: "+err.Error(), "Correct the file or access permissions and rerun wtx doctor.")
}

// Checks skipped when a prerequisite stage fails, from innermost outward.
var (
	worktreeChecks = []string{"git.compatibility", "git.branches", "shared.copy", "shared.symlink", "setup.state"}
	gitChecks      = append([]string{"git.worktrees", "git.exclude"}, worktreeChecks...)
	configChecks   = append([]string{"scripts", "teardown", "disk", "migration.references", "claude.hooks"}, gitChecks...)
)

func (s *inspection) blocked(ids ...string) {
	for _, id := range ids {
		s.add(id, "fail", "", "Check blocked by unavailable project configuration or Git worktree information.", "Resolve the prerequisite findings and rerun wtx doctor.")
	}
}

func inspect(ctx context.Context, opts Options) *inspection {
	s := &inspection{report: Report{SchemaVersion: 1, Scope: "project", Findings: []Finding{}, Repairs: []RepairOutcome{}}, seenSettings: map[string]bool{}, seenScan: map[string]bool{}, seenLinks: map[string]bool{}, resolvedDirs: map[string]string{}}
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
	where := start
	var guard snapshot
	if err == nil {
		where = filepath.Join(s.root, config.ConfigFileName)
		guard, err = takeSnapshot(where)
	}
	if err == nil {
		s.cfg, err = config.Load(s.root)
	}
	if err == nil {
		err = guard.unchanged()
	}
	if err != nil {
		s.problem("project.config", where, err)
		s.blocked(configChecks...)
		return s
	}
	s.guards = append(s.guards, guard)
	gitDir := project.GitDirPath(s.root, s.cfg)
	s.gitDir = resolved(gitDir)
	s.backups = filepath.Join(gitDir, backupDirName)
	defer s.scanLimitations(s.root)
	s.add("project.config", "ok", where, "Project configuration is readable.", "")
	s.scripts()
	s.disk()
	s.teardown(s.root)
	s.claudeSettings(s.root)
	s.scanProject()
	s.runner = git.NewRunner(gitDir, false)
	s.runner.Quiet = true
	s.gitVersion(ctx)
	if _, err := s.runner.Query(ctx, "rev-parse", "--git-dir"); err != nil {
		s.problem("project.git", gitDir, err)
		s.blocked(gitChecks...)
		return s
	}
	s.add("project.git", "ok", gitDir, "Git directory is usable.", "")
	s.exclusions()
	worktrees, err := s.runner.WorktreeList(ctx)
	if err != nil {
		s.problem("git.worktrees", gitDir, err)
		s.blocked(worktreeChecks...)
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
		s.claudeSettings(wt.Path)
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
