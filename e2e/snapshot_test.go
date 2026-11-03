package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"
)

// snapshotKey is the Env.Values key for a script's snapshotState.
type snapshotKey struct{}

// snapshotState holds the manifest the last `snapshot` recorded.
type snapshotState struct{ before map[string]string }

// cmdSnapshot records every path under $WORK (mode plus content hash, or
// symlink target) so a later `unchanged` can prove a command wrote nothing.
func cmdSnapshot(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) > 0 {
		ts.Fatalf("usage: snapshot")
	}
	m, err := manifest(ts.Getenv("WORK"))
	ts.Check(err)
	ts.Value(snapshotKey{}).(*snapshotState).before = m
}

// cmdUnchanged fails, listing each difference, if anything under $WORK
// differs from the last snapshot.
func cmdUnchanged(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) > 0 {
		ts.Fatalf("usage: unchanged")
	}
	state := ts.Value(snapshotKey{}).(*snapshotState)
	if state.before == nil {
		ts.Fatalf("unchanged: no snapshot taken")
	}
	after, err := manifest(ts.Getenv("WORK"))
	ts.Check(err)
	if diffs := diffManifests(state.before, after); len(diffs) > 0 {
		ts.Fatalf("$WORK changed since snapshot:\n%s", strings.Join(diffs, "\n"))
	}
}

// manifest maps each path under root (slash-separated, relative) to a string
// that changes whenever the entry's type, mode, content or link target does.
func manifest(root string) (map[string]string, error) {
	m := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ignoredBySnapshot(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			m[rel] = "link -> " + target
		case d.IsDir():
			m[rel] = "dir " + info.Mode().String()
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			m[rel] = "file " + info.Mode().String() + " " + hex.EncodeToString(sum[:8])
		}
		return nil
	})
	return m, err
}

// ignoredBySnapshot skips testscript's own scratch directory. Nothing else
// is ignored: testenv.GitVars turns off the index refresh and background
// maintenance that would otherwise change git's files under a read-only
// command.
func ignoredBySnapshot(rel string) bool {
	return rel == ".tmp"
}

func diffManifests(before, after map[string]string) []string {
	var diffs []string
	for path, was := range before {
		now, ok := after[path]
		switch {
		case !ok:
			diffs = append(diffs, "removed  "+path)
		case now != was:
			diffs = append(diffs, fmt.Sprintf("changed  %s (%s -> %s)", path, was, now))
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			diffs = append(diffs, "added    "+path)
		}
	}
	slices.Sort(diffs)
	return diffs
}
