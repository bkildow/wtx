package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bkildow/wtx/internal/claude"
	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

const scanLimit = 1 << 20

var legacyIdentifiers = regexp.MustCompile(`\b(wt|WT_(THEME|NO_DISK_WARN|SCRIPT_NAME|PROJECT_ROOT|SHARED_PATH|MAIN_BRANCH|MAIN_WORKTREE_PATH|WORKTREE_PATH|WORKTREE_ID|BRANCH_NAME))\b`)

// scanScope controls which candidates a scanned file can produce.
type scanScope int

const (
	scopeProject scanScope = iota // wtx-managed files: config, scripts, bin/, shared/
	scopeTracked                  // application files tracked in a worktree
	scopeUser                     // user startup files
)

// Bare "wt" is a common identifier in source code, so tracked files report it
// only when they are shell-like; WT_* names are distinctive and always reported.
func shellLike(path string, data []byte) bool {
	if bytes.HasPrefix(data, []byte("#!")) {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sh", ".bash", ".zsh", ".fish", ".ksh", ".mk", ".yml", ".yaml", ".md":
		return true
	}
	switch filepath.Base(path) {
	case "Makefile", "GNUmakefile", "Justfile", "justfile", ".envrc":
		return true
	}
	return false
}

func (s *inspection) settings(path string) {
	actual, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		// Missing optional files are normal; broken settings links are not.
		if _, linkErr := os.Lstat(path); linkErr == nil {
			s.add("claude.hooks", "warn", path, "Settings link is broken.", "Review and repair the settings link manually.")
		}
		return
	}
	if err != nil {
		s.problem("claude.hooks", path, err)
		return
	}
	if s.seenSettings[actual] {
		return
	}
	s.seenSettings[actual] = true
	if !within(s.root, actual) {
		s.add("claude.hooks", "warn", path, "Settings resolve outside the project.", "Review legacy hook commands in the external settings manually.")
		return
	}
	file, err := takeSnapshot(actual)
	if err != nil {
		s.problem("claude.hooks", actual, err)
		return
	}
	updated, changed, unresolved, err := claude.MigrateLegacyHooks(file.data)
	if errors.Is(err, claude.ErrManualMigration) {
		s.add("claude.hooks.manual", "warn", actual, "Legacy hook commands cannot be rewritten in place safely.", "Replace wt with wtx in the hook commands manually before v1.0.")
		return
	}
	if err != nil {
		s.problem("claude.hooks", actual, err)
		return
	}
	if unresolved > 0 {
		s.add("claude.hooks.manual", "warn", actual, "Legacy hooks require a replacement executable that is not available.", "Install wtx on PATH for bare wt commands, or an executable sibling wtx for absolute paths; otherwise edit the command manually before v1.0.")
	}
	if changed == 0 {
		if unresolved == 0 {
			s.add("claude.hooks", "ok", actual, "No recognized legacy hooks need migration.", "")
		}
		return
	}
	i := s.add("claude.hooks", "warn", actual, fmt.Sprintf("%d recognized legacy hook command(s) can be migrated.", changed), "Run wtx doctor --fix to replace recognized wt executables before v1.0.")
	s.plan(i, file, updated, "", "", func() error {
		if resolved(path) != actual {
			return fmt.Errorf("settings link changed since inspection: %s", path)
		}
		now, n, _, err := claude.MigrateLegacyHooks(file.data)
		if err != nil {
			return err
		}
		if n != changed || !bytes.Equal(now, updated) {
			return fmt.Errorf("hook executable availability changed since inspection")
		}
		return nil
	})
}

func skippedName(name string) bool {
	switch name {
	case ".git", ".bare", "node_modules", "vendor", "dist", "build", "target", ".next", ".cache", ".venv", "venv", "__pycache__", "coverage":
		return true
	}
	return repairArtifact(name)
}

func repairArtifact(name string) bool {
	return strings.Contains(name, ".wtx-backup-") || strings.HasPrefix(name, ".wtx-doctor-")
}

func (s *inspection) scanProject() {
	s.add("migration.scan", "ok", s.root, "Bounded candidate scan: configuration, configured scripts, bin/, shared text, root agent instructions, and tracked worktree files only. Skips Git internals, dependency/build directories, symlinks, binary/non-UTF-8 files, and files over 1 MiB; arbitrary shell includes are not followed.", "")
	for _, name := range []string{config.ConfigFileName, "AGENTS.md", "CLAUDE.md"} {
		s.scanFile(filepath.Join(s.root, name), scopeProject)
	}
	for _, path := range s.cfg.Scripts {
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		s.scanFile(path, scopeProject)
	}
	s.scanDir(filepath.Join(s.root, "bin"), false)
	if bin := project.BinPath(s.root, s.cfg); bin != filepath.Join(s.root, "bin") {
		s.scanDir(bin, false)
	}
	s.scanDir(project.SharedPath(s.root, s.cfg), true)
}

func (s *inspection) scanDir(root string, settings bool) {
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		s.add("migration.scan", "warn", root, "Directory symlink omitted from the reference scan.", "Review its target manually.")
		return
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == root {
			return nil
		}
		if err != nil {
			s.problem("migration.scan", path, err)
			return nil
		}
		if path != root && skippedName(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if settings && entry.Name() == ".claude" {
				for _, name := range []string{"settings.local.json", "settings.json"} {
					s.settings(filepath.Join(path, name))
				}
			}
			return nil
		}
		if settings && entry.Name() == ".claude" && entry.Type()&os.ModeSymlink != 0 {
			for _, name := range []string{"settings.local.json", "settings.json"} {
				s.settings(filepath.Join(path, name))
			}
		}
		s.scanFile(path, scopeProject)
		return nil
	})
	if err != nil {
		s.problem("migration.scan", root, err)
	}
}

func (s *inspection) scanTracked(ctx context.Context, root string) {
	path, err := git.WorktreeConfigPath(root)
	if err != nil {
		s.problem("migration.scan", root, err)
		return
	}
	runner := git.NewRunner(filepath.Dir(path), false)
	runner.Quiet = true
	// Explicit work-tree and bare override allow scanning before compatibility repair.
	output, err := runner.QueryRaw(ctx, "--work-tree", root, "-c", "core.bare=false", "ls-files", "-z")
	if err != nil {
		s.problem("migration.scan", root, err)
		return
	}
	for _, rel := range strings.Split(output, "\x00") {
		if rel == "" {
			continue
		}
		skip := false
		for _, name := range strings.Split(rel, "/") {
			if skippedName(name) {
				skip = true
				break
			}
		}
		if !skip {
			s.scanFile(filepath.Join(root, rel), scopeTracked)
		}
	}
}

// scanFile scans one file. Callers filter skipped directory names relative to
// their scan root; the project's own ancestors must not cause a skip.
func (s *inspection) scanFile(path string, scope scanScope) {
	if s.gitDir != "" && within(s.gitDir, resolved(path)) {
		return
	}
	if s.seenScan[path] {
		return
	}
	s.seenScan[path] = true
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		s.problem("migration.scan", path, err)
		return
	}
	if !info.Mode().IsRegular() || info.Size() > scanLimit {
		s.scanSkipped++
		return
	}
	// Refuse files reached through directory symlinks, including tracked paths.
	if resolved(filepath.Dir(path)) != filepath.Clean(filepath.Dir(path)) {
		s.scanSkipped++
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.problem("migration.scan", path, err)
		return
	}
	data, readErr := io.ReadAll(io.LimitReader(f, scanLimit+1))
	closeErr := f.Close()
	if readErr != nil {
		s.problem("migration.scan", path, readErr)
		return
	}
	if closeErr != nil {
		s.problem("migration.scan", path, closeErr)
		return
	}
	if len(data) > scanLimit || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		s.scanSkipped++
		return
	}
	bareWT := scope != scopeTracked || shellLike(path, data)
	for line, text := range strings.Split(string(data), "\n") {
		seen := map[string]bool{}
		for _, match := range legacyIdentifiers.FindAllStringIndex(text, -1) {
			identifier := text[match[0]:match[1]]
			if identifier == "wt" && !bareWT {
				continue
			}
			// ${WTX_X:-${WT_X}} style fallbacks are transition compatibility.
			if identifier != "wt" && strings.Contains(text, "WTX_"+strings.TrimPrefix(identifier, "WT_")) {
				continue
			}
			if identifier == "wt" && ((match[0] > 0 && strings.ContainsRune(".-_", rune(text[match[0]-1]))) || (match[1] < len(text) && strings.ContainsRune(".-_", rune(text[match[1]])))) {
				continue
			}
			if seen[identifier] {
				continue
			}
			seen[identifier] = true
			replacement := strings.Replace(identifier, "WT_", "WTX_", 1)
			deadline := "v1.0"
			if identifier == "wt" {
				replacement = "wtx"
			}
			if identifier == "WT_THEME" || identifier == "WT_NO_DISK_WARN" {
				deadline = "v0.12"
			}
			remedy := fmt.Sprintf("Review candidate %s reference and replace with %s before %s.", identifier, replacement, deadline)
			if scope == scopeUser && identifier == "wt" {
				if strings.Contains(text, "wt shell-init") {
					remedy = "Replace the wt shell-init startup invocation with wtx shell-init before v1.0."
				} else {
					remedy = "Review custom wrapper/command candidate wt and update to wtx before v1.0."
				}
			}
			i := s.add("migration.references", "warn", path, "Legacy identifier candidate: "+identifier+" (text match; execution not established).", remedy)
			s.report.Findings[i].Line = line + 1
		}
	}
}

// scanLimitations reports skipped files once instead of one finding per file.
func (s *inspection) scanLimitations(path string) {
	if s.scanSkipped > 0 {
		s.add("migration.scan", "ok", path, fmt.Sprintf("Scan limitation: %d non-regular, binary, non-UTF-8, oversized (over 1 MiB), or symlinked-directory file(s) skipped.", s.scanSkipped), "")
	}
}

func (s *inspection) user() {
	if path, err := exec.LookPath("wtx"); err != nil {
		s.add("user.path", "fail", "", "wtx is not executable on PATH.", "Install wtx or update PATH manually.")
	} else {
		s.add("user.path", "ok", path, "wtx is available on PATH.", "")
	}
	for _, suffix := range []string{"THEME", "NO_DISK_WARN"} {
		if value, exists := os.LookupEnv("WT_" + suffix); exists && value != "" {
			_, overridden := os.LookupEnv("WTX_" + suffix)
			message := "Active legacy input setting: WT_" + suffix
			if overridden {
				message = "Legacy input setting overridden by WTX_: WT_" + suffix
			}
			s.add("user.environment", "warn", "", message, "Replace WT_"+suffix+" with WTX_"+suffix+" before v0.12; values are intentionally omitted.")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		s.problem("user.startup", "", err)
		return
	}
	// Canonicalize the home root so normal macOS /var aliases aren't mistaken
	// for arbitrary shell includes during scanning.
	home = resolved(home)
	zdir := os.Getenv("ZDOTDIR")
	if zdir == "" {
		zdir = home
	}
	zdir = resolved(zdir)
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	xdg = resolved(xdg)
	for _, name := range []string{".bashrc", ".bash_profile", ".bash_login", ".profile"} {
		s.scanFile(filepath.Join(home, name), scopeUser)
	}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin", ".zlogout"} {
		s.scanFile(filepath.Join(zdir, name), scopeUser)
	}
	s.scanFile(filepath.Join(xdg, "fish", "config.fish"), scopeUser)
	conf := filepath.Join(xdg, "fish", "conf.d")
	entries, err := os.ReadDir(conf)
	if err != nil && !os.IsNotExist(err) {
		s.problem("user.startup", conf, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".fish") {
			s.scanFile(filepath.Join(conf, entry.Name()), scopeUser)
		}
	}
	s.scanLimitations(home)
	s.add("migration.scan", "ok", home, "User scan checks standard Bash, Zsh, and Fish startup files only; honors ZDOTDIR and XDG_CONFIG_HOME. Files are never sourced; arbitrary includes, symlinks, binary files, and files over 1 MiB are skipped.", "")
}
