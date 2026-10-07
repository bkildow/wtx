package cmd

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/bkildow/wtx/internal/doctor"
	"github.com/bkildow/wtx/internal/ui"
)

// doctorItemLimit caps the locations listed per problem without --verbose.
const doctorItemLimit = 5

// doctorCheck describes a check ID for the human report. about and remedy
// replace per-finding text when findings in a group vary only by subject.
// summarize hides the item list without --verbose (cleanup hints, not health
// problems); note marks informational checks that only appear with --verbose
// when they pass, since they verify nothing; hint shows a passing group's
// remedy (an optional suggestion) without --verbose.
type doctorCheck struct {
	id, title, about, remedy string
	summarize, note, hint    bool
}

// doctorChecks lists checks in report order.
var doctorChecks = []doctorCheck{
	{id: "project.config", title: "Project config"},
	{id: "project.git", title: "Git directory"},
	{id: "git.version", title: "Git version"},
	{id: "git.compatibility", title: "Bare repository compatibility"},
	{id: "git.exclude", title: "Git exclude file"},
	{id: "git.worktrees", title: "Worktrees"},
	{id: "home.paths", title: "Worktree directory"},
	{id: "home.marker", title: "~/.wtx ownership marker"},
	{id: "home.layout", title: "Worktree layout", hint: true},
	{id: "home.migrate", title: "Move to ~/.wtx"},
	{
		id: "home.orphans", title: "Orphaned ~/.wtx directories",
		about: "The repository that owned each directory is gone or no longer a wtx project.",
	},
	{
		id: "git.branches", title: "Local branches with no worktree or remote",
		about:     "Not checked out anywhere and no matching remote-tracking branch; often merged or abandoned work.",
		summarize: true,
	},
	{id: "shared.copy", title: "Shared copies"},
	{id: "shared.symlink", title: "Shared symlinks"},
	{id: "setup.state", title: "Setup state"},
	{id: "scripts", title: "Scripts"},
	{id: "teardown", title: "Teardown hooks"},
	{id: "disk", title: "Disk space"},
	{id: "claude.hooks", title: "Claude hooks"},
	{id: "claude.hooks.manual", title: "Claude hooks needing manual edits"},
	{
		id: "migration.references", title: "Legacy wt references",
		about:  "Text matches for the old name; not every match is executed.",
		remedy: "Replace wt with wtx and WT_* with WTX_* (WT_THEME and WT_NO_DISK_WARN before v0.12, the rest before v1.0).",
	},
	{id: "migration.scan", title: "Reference scan", note: true},
	{id: "user.repair", title: "User repairs"},
	{id: "user.path", title: "wtx on PATH"},
	{id: "user.environment", title: "Legacy environment variables"},
	{id: "user.startup", title: "Shell startup files"},
}

func lookupDoctorCheck(id string) (int, doctorCheck) {
	for i, c := range doctorChecks {
		if c.id == id {
			return i, c
		}
	}
	return len(doctorChecks), doctorCheck{id: id, title: id}
}

type doctorGroup struct {
	check    doctorCheck
	order    int
	severity doctor.Severity
	findings []doctor.Finding
}

func (g doctorGroup) fixable() bool {
	return slices.ContainsFunc(g.findings, func(f doctor.Finding) bool { return f.Repairable })
}

// groupDoctorFindings buckets findings by check and severity, most severe first.
func groupDoctorFindings(findings []doctor.Finding) []doctorGroup {
	index := map[[2]string]int{}
	var groups []doctorGroup
	for _, f := range findings {
		key := [2]string{f.ID, string(f.Severity)}
		i, ok := index[key]
		if !ok {
			order, check := lookupDoctorCheck(f.ID)
			i = len(groups)
			index[key] = i
			groups = append(groups, doctorGroup{check: check, order: order, severity: f.Severity})
		}
		groups[i].findings = append(groups[i].findings, f)
	}
	rank := map[doctor.Severity]int{doctor.Fail: 0, doctor.Warn: 1, doctor.OK: 2}
	sort.SliceStable(groups, func(i, j int) bool {
		if rank[groups[i].severity] != rank[groups[j].severity] {
			return rank[groups[i].severity] < rank[groups[j].severity]
		}
		return groups[i].order < groups[j].order
	})
	return groups
}

type doctorRenderer struct {
	w       io.Writer
	root    string
	verbose bool
}

func renderDoctorReport(w io.Writer, r doctor.Report, verbose bool) {
	d := doctorRenderer{w: w, root: r.Root, verbose: verbose}
	groups := groupDoctorFindings(r.Findings)

	var problems, passed []doctorGroup
	failing := map[string]bool{}
	for _, g := range groups {
		if g.severity != doctor.OK {
			problems = append(problems, g)
			failing[g.check.id] = true
		}
	}
	var hints []doctorGroup
	for _, g := range groups {
		if g.severity != doctor.OK || failing[g.check.id] || g.check.note {
			continue
		}
		if g.check.hint && slices.ContainsFunc(g.findings, func(f doctor.Finding) bool { return f.Remedy != "" }) {
			hints = append(hints, g)
			continue
		}
		passed = append(passed, g)
	}

	d.summary(r, problems, passed)
	for _, g := range problems {
		d.group(g)
	}
	if verbose {
		for _, g := range groups {
			if g.severity == doctor.OK {
				d.group(g)
			}
		}
	} else {
		for _, g := range hints {
			d.group(g)
		}
	}
	if !verbose && len(passed) > 0 {
		titles := make([]string, len(passed))
		for i, g := range passed {
			titles[i] = g.check.title
		}
		d.line("")
		d.line(ui.StyleSuccess.Render("✓ Passed: ") + ui.StyleMuted.Render(strings.Join(titles, ", ")))
	}
	d.repairs(r.Repairs)
	d.nextSteps(r, problems)
}

func (d doctorRenderer) line(s string) {
	_, _ = lipgloss.Fprintln(d.w, s)
}

func (d doctorRenderer) summary(r doctor.Report, problems, passed []doctorGroup) {
	title := "wtx doctor"
	if r.Scope == "user" {
		title += " --user"
	}
	if r.Root != "" {
		title += "  " + ui.StyleMuted.Render(ui.DisplayPath("", r.Root))
	}
	d.line(ui.StyleHeading.Render(title))

	fails, warns := 0, 0
	for _, g := range problems {
		if g.severity == doctor.Fail {
			fails++
		} else {
			warns++
		}
	}
	var parts []string
	if fails > 0 {
		parts = append(parts, ui.StyleError.Render(plural(fails, "failure", "failures")))
	}
	if warns > 0 {
		parts = append(parts, ui.StyleWarning.Render(plural(warns, "warning", "warnings")))
	}
	parts = append(parts, ui.StyleSuccess.Render(plural(len(passed), "check passed", "checks passed")))
	d.line(strings.Join(parts, ui.StyleMuted.Render(" · ")))
}

func (d doctorRenderer) group(g doctorGroup) {
	icon, style := "✓", ui.StyleSuccess
	switch g.severity {
	case doctor.Fail:
		icon, style = "✗", ui.StyleError
	case doctor.Warn:
		icon, style = "⚠", ui.StyleWarning
	}
	header := style.Render(icon + " " + g.check.title)
	if g.severity != doctor.OK && len(g.findings) > 1 {
		count := fmt.Sprintf(" (%d)", len(g.findings))
		if paths := distinct(g.findings, func(f doctor.Finding) string { return f.Path }); len(paths) > 1 && len(paths) < len(g.findings) {
			count = fmt.Sprintf(" (%d in %d files)", len(g.findings), len(paths))
		}
		header += ui.StyleMuted.Render(count)
	}
	if g.fixable() {
		header += ui.StyleInfo.Render("  [fixable]")
	}
	d.line("")
	d.line(header)

	explanations := distinct(g.findings, func(f doctor.Finding) string { return f.Explanation })
	about := g.check.about
	if about == "" && len(explanations) == 1 {
		about = explanations[0]
	}
	if about != "" {
		d.line("    " + about)
	}

	items := d.items(g, about == "")
	limit := len(items)
	if !d.verbose && g.check.summarize {
		limit = 0
	} else if !d.verbose && limit > doctorItemLimit+1 {
		limit = doctorItemLimit
	}
	for _, item := range items[:limit] {
		d.line(ui.StyleMuted.Render("    " + item))
	}
	switch {
	case limit == 0 && len(items) > 0:
		d.line(ui.StyleMuted.Render("    --verbose lists them"))
	case limit < len(items):
		d.line(ui.StyleMuted.Render(fmt.Sprintf("    … and %d more (--verbose lists all)", len(items)-limit)))
	}

	remedies := []string{g.check.remedy}
	if g.check.remedy == "" {
		remedies = distinct(g.findings, func(f doctor.Finding) string { return f.Remedy })
	}
	for _, remedy := range remedies {
		if remedy != "" {
			d.line(ui.StyleInfo.Render("  → " + remedy))
		}
	}
}

// items lists what each finding in a group refers to. Findings that share a
// path collapse into one entry; findings that share one path list subjects.
func (d doctorRenderer) items(g doctorGroup, explain bool) []string {
	type entry struct {
		path     string
		lines    []int
		subjects []string
		explain  []string
	}
	var entries []*entry
	byPath := map[string]*entry{}
	for _, f := range g.findings {
		e := byPath[f.Path]
		if e == nil {
			e = &entry{path: f.Path}
			byPath[f.Path] = e
			entries = append(entries, e)
		}
		if f.Line > 0 && !slices.Contains(e.lines, f.Line) {
			e.lines = append(e.lines, f.Line)
		}
		if f.Subject != "" && !slices.Contains(e.subjects, f.Subject) {
			e.subjects = append(e.subjects, f.Subject)
		}
		if !slices.Contains(e.explain, f.Explanation) {
			e.explain = append(e.explain, f.Explanation)
		}
	}

	// One shared location (e.g. the Git directory for branches): list subjects.
	if len(entries) == 1 && len(entries[0].subjects) > 1 {
		return entries[0].subjects
	}
	subjects := distinct(g.findings, func(f doctor.Finding) string { return f.Subject })
	sort.SliceStable(entries, func(i, j int) bool { return len(entries[i].lines) > len(entries[j].lines) })

	var items []string
	for _, e := range entries {
		item := d.display(e.path)
		switch {
		case len(e.lines) == 1:
			item += fmt.Sprintf(":%d", e.lines[0])
		case len(e.lines) > 1:
			item += fmt.Sprintf(" (%d lines)", len(e.lines))
		}
		if len(subjects) > 1 && len(e.subjects) > 0 {
			item += " — " + joinLimited(e.subjects, 3)
		} else if explain {
			item = strings.TrimPrefix(item+" — "+strings.Join(e.explain, " "), " — ")
		}
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (d doctorRenderer) repairs(repairs []doctor.RepairOutcome) {
	if len(repairs) == 0 {
		return
	}
	d.line("")
	d.line(ui.StyleHeading.Render("Repairs"))
	for _, r := range repairs {
		_, check := lookupDoctorCheck(r.ID)
		where := ui.StyleMuted.Render(" " + d.display(r.Path))
		switch r.Status {
		case doctor.Planned:
			d.line(ui.StyleInfo.Render("  • would repair "+check.title) + where)
		case doctor.Applied:
			d.line(ui.StyleSuccess.Render("  ✓ repaired "+check.title) + where)
		case doctor.Failed:
			d.line(ui.StyleError.Render("  ✗ could not repair "+check.title) + where)
		}
		if r.Action != "" {
			d.line(ui.StyleMuted.Render("      " + r.Action))
		}
		if r.Backup != "" {
			d.line(ui.StyleMuted.Render("      backup: " + d.display(r.Backup)))
		}
		if r.Error != "" {
			d.line(ui.StyleError.Render("      " + r.Error))
		}
	}
}

func (d doctorRenderer) nextSteps(r doctor.Report, problems []doctorGroup) {
	fixable := 0
	for _, g := range problems {
		if g.fixable() {
			fixable++
		}
	}
	var steps []string
	if fixable > 0 && len(r.Repairs) == 0 {
		steps = append(steps, fmt.Sprintf("Run wtx doctor --fix to repair %s automatically (add --dry-run to preview).", plural(fixable, "item", "items")))
	}
	if !d.verbose && len(problems) > 0 {
		steps = append(steps, "Run with --verbose for every finding, or --json for a structured report.")
	}
	if len(steps) == 0 {
		return
	}
	d.line("")
	for _, s := range steps {
		d.line(ui.StyleInfo.Render(s))
	}
}

// display shortens a path relative to the project root or home directory.
func (d doctorRenderer) display(path string) string {
	return ui.DisplayPath(d.root, path)
}

func distinct(findings []doctor.Finding, key func(doctor.Finding) string) []string {
	var out []string
	for _, f := range findings {
		if k := key(f); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

func joinLimited(values []string, limit int) string {
	if len(values) <= limit {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(values[:limit], ", "), len(values)-limit)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
