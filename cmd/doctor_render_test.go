package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/doctor"
)

func TestRenderDoctorReportGroupsFindings(t *testing.T) {
	root := "/work/proj"
	report := doctor.Report{Scope: "project", Root: root}
	add := func(f doctor.Finding) { report.Findings = append(report.Findings, f) }
	add(doctor.Finding{ID: "project.config", Severity: doctor.OK, Path: root + "/.worktree.yml", Explanation: "Project configuration is readable."})
	add(doctor.Finding{ID: "git.exclude", Severity: doctor.Warn, Path: root + "/.git/info/exclude", Explanation: "Managed exclusions are stale.", Remedy: "Run wtx doctor --fix.", Repairable: true})
	for i := range 8 {
		branch := fmt.Sprintf("topic-%d", i)
		add(doctor.Finding{ID: "git.branches", Severity: doctor.Warn, Path: root + "/.git", Subject: branch, Explanation: "Local branch " + branch + " is stale.", Remedy: "Review the branch."})
	}
	for line := 1; line <= 30; line++ {
		add(doctor.Finding{ID: "migration.references", Severity: doctor.Warn, Path: root + "/README.md", Line: line, Subject: "wt", Explanation: "Legacy identifier candidate: wt.", Remedy: "Replace wt."})
	}
	add(doctor.Finding{ID: "migration.references", Severity: doctor.Warn, Path: root + "/run.sh", Line: 3, Subject: "WT_THEME", Explanation: "Legacy identifier candidate: WT_THEME.", Remedy: "Replace WT_THEME."})
	add(doctor.Finding{ID: "git.version", Severity: doctor.Fail, Explanation: "Git 2.10.0", Remedy: "Upgrade Git."})
	add(doctor.Finding{ID: "migration.scan", Severity: doctor.OK, Path: root, Explanation: "Bounded candidate scan."})

	var buf bytes.Buffer
	renderDoctorReport(&buf, report, false)
	out := buf.String()

	for _, want := range []string{
		"1 failure · 3 warnings · 1 check passed",
		"✗ Git version\n    Git 2.10.0\n  → Upgrade Git.",
		"⚠ Git exclude file  [fixable]",
		"⚠ Local branches with no worktree or remote (8)\n    Not checked out anywhere and no matching remote-tracking branch; often merged or abandoned work.\n    --verbose lists them\n  → Review the branch.",
		"⚠ Legacy wt references (31 in 2 files)",
		"    README.md (30 lines) — wt\n    run.sh:3 — WT_THEME\n",
		"✓ Passed: Project config",
		"Run wtx doctor --fix to repair 1 item automatically",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Reference scan") {
		t.Errorf("passing informational checks belong in --verbose only:\n%s", out)
	}
	if strings.Contains(out, "topic-0") || strings.Contains(out, "topic-7") || strings.Contains(out, "Legacy identifier candidate") || strings.Contains(out, root+"/") {
		t.Errorf("output not collapsed:\n%s", out)
	}
	if strings.Index(out, "Git version") > strings.Index(out, "Git exclude") {
		t.Errorf("failures should precede warnings:\n%s", out)
	}

	buf.Reset()
	renderDoctorReport(&buf, report, true)
	if out := buf.String(); !strings.Contains(out, "topic-7") || !strings.Contains(out, "✓ Project config\n    Project configuration is readable.") || !strings.Contains(out, "✓ Reference scan") {
		t.Errorf("verbose output should list everything:\n%s", out)
	}
}

func TestRenderDoctorReportHint(t *testing.T) {
	report := doctor.Report{Scope: "project", Root: "/p", Findings: []doctor.Finding{
		{ID: "home.layout", Severity: doctor.OK, Path: "/p/.worktrees", Explanation: "Worktrees live inside the repository (in-repo layout).", Remedy: "Optional: migrate."},
		{ID: "home.paths", Severity: doctor.OK, Path: "/p/.worktrees", Explanation: "Worktree directory exists and is writable."},
	}}
	var buf bytes.Buffer
	renderDoctorReport(&buf, report, false)
	out := buf.String()
	for _, want := range []string{"✓ Worktree layout\n    Worktrees live inside the repository (in-repo layout).", "→ Optional: migrate.", "✓ Passed: Worktree directory\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderDoctorReportRepairs(t *testing.T) {
	report := doctor.Report{Scope: "project", Root: "/p", Repairs: []doctor.RepairOutcome{
		{ID: "git.exclude", Path: "/p/.git/info/exclude", Status: doctor.Applied, Backup: "/p/.git/wtx-doctor-backups/1"},
		{ID: "claude.hooks", Path: "/p/.claude/settings.json", Status: doctor.Failed, Error: "input changed"},
	}}
	var buf bytes.Buffer
	renderDoctorReport(&buf, report, false)
	out := buf.String()
	for _, want := range []string{
		"✓ repaired Git exclude file .git/info/exclude\n      backup: .git/wtx-doctor-backups/1",
		"✗ could not repair Claude hooks .claude/settings.json\n      input changed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--fix to repair") {
		t.Errorf("should not suggest --fix after repairs:\n%s", out)
	}
}
