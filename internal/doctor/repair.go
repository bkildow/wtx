package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/git"
)

// backupDirName lives under the Git directory, which scans and apply skip.
const backupDirName = "wtx-doctor-backups"

type snapshot struct {
	path   string
	data   []byte
	info   os.FileInfo
	parent string
}

func takeSnapshot(path string) (snapshot, error) {
	s := snapshot{path: path}
	// Resolve the nearest existing ancestor, including when info/ is absent.
	parent := filepath.Dir(path)
	for {
		p, err := filepath.EvalSymlinks(parent)
		if err == nil {
			s.parent = p
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(parent) == parent {
			return s, err
		}
		parent = filepath.Dir(parent)
	}
	var err error
	s.info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !s.info.Mode().IsRegular() {
		return s, fmt.Errorf("refusing non-regular repair input: %s", path)
	}
	s.data, err = os.ReadFile(path)
	return s, err
}

func (s snapshot) unchanged() error {
	now, err := takeSnapshot(s.path)
	if err != nil {
		return err
	}
	if now.parent != s.parent || (now.info == nil) != (s.info == nil) {
		return fmt.Errorf("input changed since inspection: %s", s.path)
	}
	if s.info != nil && (!os.SameFile(s.info, now.info) || s.info.Mode() != now.info.Mode() || !s.info.ModTime().Equal(now.info.ModTime()) || !bytes.Equal(s.data, now.data)) {
		return fmt.Errorf("input changed since inspection: %s", s.path)
	}
	return nil
}

type repair struct {
	id       string
	file     snapshot
	guards   []snapshot
	data     []byte
	key      string
	value    string
	backups  string
	validate func() error
}

// planContent schedules replacing file with data.
func (s *inspection) planContent(index int, file snapshot, data []byte, validate func() error) {
	s.plan(index, repair{file: file, data: data, validate: validate})
}

// planConfig schedules setting one Git config key in file.
func (s *inspection) planConfig(index int, file snapshot, key, value string, guards ...snapshot) {
	s.plan(index, repair{file: file, key: key, value: value, guards: guards})
}

func (s *inspection) plan(index int, r repair) {
	if !within(s.root, resolved(r.file.path)) || !within(s.root, r.file.parent) {
		s.report.Findings[index].Remedy += " Repair manually: target is outside the project."
		return
	}
	s.report.Findings[index].Repairable = true
	r.id = s.report.Findings[index].ID
	// Keep backups out of worktrees and shared/, where apply would copy or link them.
	r.backups = s.backups
	r.guards = append(append([]snapshot(nil), s.guards...), r.guards...)
	s.repairs = append(s.repairs, r)
}

func (r repair) check() error {
	for _, guard := range r.guards {
		if err := guard.unchanged(); err != nil {
			return err
		}
	}
	if err := r.file.unchanged(); err != nil {
		return err
	}
	if r.validate != nil {
		return r.validate()
	}
	return nil
}

func (r repair) verify(ctx context.Context) error {
	if r.key != "" {
		value, err := git.ConfigBool(ctx, r.file.path, r.key)
		if err != nil {
			return err
		}
		if value != r.value {
			return fmt.Errorf("%s was not set to %s", r.key, r.value)
		}
		return nil
	}
	data, err := os.ReadFile(r.file.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, r.data) {
		return fmt.Errorf("repaired contents changed: %s", r.file.path)
	}
	return nil
}

func (r repair) apply(ctx context.Context) (string, error) {
	if err := r.check(); err != nil {
		return "", err
	}
	dir := filepath.Dir(r.file.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// If a missing parent was created, refresh just its identity after checking
	// all original inputs. The target must still be absent/unchanged.
	current, err := takeSnapshot(r.file.path)
	if err != nil {
		return "", err
	}
	r.file.parent = current.parent
	if err := r.check(); err != nil {
		return "", err
	}
	mode := os.FileMode(0o600)
	if r.file.info != nil {
		mode = r.file.info.Mode().Perm()
	}
	data := r.data
	if r.key != "" {
		data = r.file.data
	}
	name, err := writeTemp(dir, ".wtx-doctor-*", data, mode)
	if name != "" {
		defer func() { _ = os.Remove(name) }()
	}
	if err != nil {
		return "", err
	}
	if r.key != "" {
		if err := git.SetConfigFile(ctx, name, r.key, r.value); err != nil {
			return "", err
		}
		// git config replaces the file via rename; restore the mode.
		if err := os.Chmod(name, mode); err != nil {
			return "", err
		}
	}
	if err := r.check(); err != nil {
		return "", err
	}
	backup := ""
	if r.file.info != nil {
		if err := os.MkdirAll(r.backups, 0o700); err != nil {
			return "", err
		}
		if backup, err = writeTemp(r.backups, filepath.Base(r.file.path)+".wtx-backup-*", r.file.data, mode); err != nil {
			return backup, err
		}
	}
	if err := r.check(); err != nil {
		return backup, err
	}
	return backup, os.Rename(name, r.file.path)
}

// writeTemp writes data to a new uniquely named file in dir with mode. It
// returns the name whenever the file was created, even on error.
func writeTemp(dir, pattern string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	modeErr := f.Chmod(mode)
	closeErr := f.Close()
	return f.Name(), errors.Join(writeErr, modeErr, closeErr)
}
