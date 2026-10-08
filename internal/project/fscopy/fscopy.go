// Package fscopy copies files, preferring a filesystem-level reflink
// (copy-on-write clone) when the platform and filesystem support it and
// falling back to a byte-for-byte copy otherwise.
//
// Reflink turns copying a large file into a near-instant extent-sharing
// operation: the destination shares on-disk blocks with the source until
// one side is modified. This matters most for large, rarely-modified
// directories like vendor/, node_modules/, .venv/, and target/ in
// shared/copy/. Users should expect fast completion for big copies —
// that's the reflink working, not a bug.
//
// On Darwin, unix.Clonefile with flag 0 also preserves extended
// attributes and ACLs on top of the mode bits that the byte-copy fallback
// carries over. This is a forgiving superset of the fallback's behavior.
package fscopy

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// errReflinkUnsupported signals that tryReflink could not clone on this
// platform/filesystem pair and CopyFile should fall back to a byte copy.
// It is intentionally unexported: CopyFile handles the fallback itself,
// so no caller needs to distinguish this case.
var errReflinkUnsupported = errors.New("fscopy: reflink not supported")

// CopyFile copies src to dst. It first tries a filesystem-level reflink;
// if the underlying filesystem or platform does not support cloning, it
// falls back to a byte-for-byte copy. The destination inherits the
// source's mode bits.
//
// CopyFile writes into "<dst>.wtclone" and atomically renames it into
// place, so an interrupted run never leaves a half-written dst. Any
// leftover tmp file from a prior crash is removed before the new attempt.
func CopyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	tmp := dst + ".wtclone"
	_ = os.Remove(tmp)

	if err := tryReflink(src, tmp); err != nil {
		if !errors.Is(err, errReflinkUnsupported) {
			_ = os.Remove(tmp)
			return fmt.Errorf("fscopy: reflink %s -> %s: %w", src, dst, err)
		}
		if err := byteCopy(src, tmp, srcInfo.Mode()); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}

	if err := os.Chmod(tmp, srcInfo.Mode()); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// CopyTree clones a directory in one filesystem reflink syscall, writing via
// "<dst>.wtclone" + atomic rename. Returns an error for which IsReflinkUnsupported
// is true when the FS can't clone trees; callers fall back to a per-file walk.
func CopyTree(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}

	tmp := dst + ".wtclone"
	_ = os.RemoveAll(tmp)

	if err := tryCloneTree(src, tmp); err != nil {
		if errors.Is(err, errReflinkUnsupported) {
			return err
		}
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("fscopy: clone tree %s -> %s: %w", src, dst, err)
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// IsReflinkUnsupported reports whether err indicates the platform or
// filesystem does not support reflink/tree-clone. Callers of CopyTree
// use this to decide whether to fall back to a per-file walk.
func IsReflinkUnsupported(err error) bool {
	return errors.Is(err, errReflinkUnsupported)
}

// byteCopy performs the non-reflink fallback: a plain io.Copy preserving
// the source mode. Matches the semantics of the previous private copyFile
// in internal/project/apply.go so existing callers see no behavior change
// on platforms/filesystems without reflink support.
func byteCopy(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// CopySymlink recreates the symlink at src as dst, preserving its target.
func CopySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(target, dst)
}
