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
	"strings"
)

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

type swap struct {
	target     string
	backup     string
	done       []swapped
	incomplete bool
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
		if hadPrevious {
			_ = os.Rename(filepath.Join(s.backup, name), dst)
		}
		return fmt.Errorf("install %s: %w", name, err)
	}
	s.done = append(s.done, swapped{name: name, hadPrevious: hadPrevious})
	return nil
}

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
	s.incomplete = len(errs) > 0
	return errs
}

const (
	stagePrefix  = ".gplay-stage-"
	backupPrefix = ".gplay-backup-"
)

func sweepOrphans(target string) []error {
	entries, err := os.ReadDir(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return []error{fmt.Errorf("scan %s for leftovers: %w", target, err)}
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(target, name)
		switch {
		case strings.HasPrefix(name, backupPrefix):
			errs = append(errs, adoptBackup(path, target)...)
		case strings.HasPrefix(name, stagePrefix):
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, fmt.Errorf("remove leftover staging directory %s: %w", path, err))
			}
		}
	}
	return errs
}

func adoptBackup(dir, target string) []error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []error{fmt.Errorf("read leftover backup %s: %w", dir, err)}
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(target, e.Name())
		if _, err := os.Lstat(dst); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("inspect %s: %w", dst, err))
			continue
		}
		if err := os.Rename(filepath.Join(dir, e.Name()), dst); err != nil {
			errs = append(errs, fmt.Errorf("restore %s from leftover backup: %w", e.Name(), err))
		}
	}
	if len(errs) > 0 {
		return errs
	}
	if err := os.RemoveAll(dir); err != nil {
		errs = append(errs, fmt.Errorf("remove leftover backup %s: %w", dir, err))
	}
	return errs
}
