package installskills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// copyTree copies src into dst, creating dst. Only directories and regular
// files are accepted: a symlink in the pack would resolve at *install* time
// against the user's filesystem, so it is refused outright rather than followed
// or silently skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(path, target)
		default:
			return fmt.Errorf("%s is not a regular file (%s)", rel, d.Type())
		}
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src is inside the pinned checkout we just created
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	// Executable bit is preserved (a skill may ship a helper script), the rest
	// is normalised so a permissive mode in the pack cannot widen the install.
	mode := os.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) // #nosec G304 -- dst is built from the validated skill name
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// fileDigests returns every regular file under root, keyed by slash-separated
// relative path, with its SHA-256. A non-regular entry is an error: the same
// rule as copyTree, so verification cannot pass on a tree copyTree would have
// refused.
func fileDigests(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%s)", path, d.Type())
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum, err := digest(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sum
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func digest(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from a WalkDir of a directory we control
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyTree asserts dst holds exactly src's files, byte for byte. This is the
// per-file half of the acceptance criteria: the pack-level check proves the
// right skills are there, this proves each one arrived intact and complete
// (a truncated copy, a missing file, or a stray leftover all fail here).
func verifyTree(src, dst string) error {
	want, err := fileDigests(src)
	if err != nil {
		return fmt.Errorf("hash source: %w", err)
	}
	got, err := fileDigests(dst)
	if err != nil {
		return fmt.Errorf("hash installed copy: %w", err)
	}
	var problems []string
	for rel, sum := range want {
		switch installed, ok := got[rel]; {
		case !ok:
			problems = append(problems, "missing "+rel)
		case installed != sum:
			problems = append(problems, "content differs for "+rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			problems = append(problems, "unexpected "+rel)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New(joinOrNone(problems))
	}
	return nil
}

// swap moves the staged pack into place, one skill at a time, keeping enough
// state to undo every move it has already made.
//
// The unit of replacement is the skill directory, so skills the pack does not
// name are never touched: whatever else lives in the target directory (a
// hand-written skill, another tool's pack) survives untouched.
type swap struct {
	target string // the agent-skills directory being written to
	backup string // where displaced skill directories are parked
	// done records, in order, the skills already swapped in, so a failure
	// halfway through can be walked back in reverse.
	done []swapped
}

type swapped struct {
	name        string
	hadPrevious bool
}

func (s *swap) install(name, staged string) error {
	dst := filepath.Join(s.target, name)
	hadPrevious := false
	if _, err := os.Lstat(dst); err == nil {
		hadPrevious = true
		if err := os.Rename(dst, filepath.Join(s.backup, name)); err != nil {
			return fmt.Errorf("set aside existing %s: %w", name, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect existing %s: %w", name, err)
	}
	if err := os.Rename(staged, dst); err != nil {
		// The displaced original is still in the backup dir; record nothing for
		// this skill and let rollback put it back.
		if hadPrevious {
			_ = os.Rename(filepath.Join(s.backup, name), dst)
		}
		return fmt.Errorf("install %s: %w", name, err)
	}
	s.done = append(s.done, swapped{name: name, hadPrevious: hadPrevious})
	return nil
}

// rollback undoes every completed swap, newest first, restoring the previous
// skill directory where there was one and removing ours where there was not.
// Errors are collected, not returned: rollback runs on an already-failing path
// and must attempt every entry.
func (s *swap) rollback() []error {
	var errs []error
	for i := len(s.done) - 1; i >= 0; i-- {
		e := s.done[i]
		dst := filepath.Join(s.target, e.name)
		if err := os.RemoveAll(dst); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", e.name, err))
			continue
		}
		if e.hadPrevious {
			if err := os.Rename(filepath.Join(s.backup, e.name), dst); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", e.name, err))
			}
		}
	}
	s.done = nil
	return errs
}
