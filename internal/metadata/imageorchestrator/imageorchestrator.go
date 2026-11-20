package imageorchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagevalidate"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

type Opts struct {
	Package string

	Dir string

	DryRun bool

	Confirm bool

	Locales []string
	Types   []string

	Prune bool

	NoValidate bool

	ExplicitEditID string

	Commit edits.CommitOptions

	AIGenerated images.AIGeneratedState
}

type Result struct {
	Package string
	DryRun  bool
	Diff    imagediff.Result
}

func confirmRequired() error {
	return exit.SafetyFlag("confirm", "metadata images apply publishes images live to the store immediately; pass --confirm to proceed (preview first with --dry-run)")
}

type ValidationError struct{ Problems []imagevalidate.Problem }

func (e *ValidationError) Error() string {
	n := len(e.Problems)
	msg := "image validation failed before apply"
	if n > 0 {
		msg += ": " + e.Problems[0].ImageType + " (" + e.Problems[0].Rule + ") " + e.Problems[0].Message
		if n > 1 {
			msg += " (+ more; run `gplay metadata images validate`)"
		}
	}
	return msg
}
func (e *ValidationError) ExitCode() int { return 20 }

type LocaleError struct {
	Locale string
	Err    error
}

func (e *LocaleError) Error() string { return e.Err.Error() }
func (e *LocaleError) Unwrap() error { return e.Err }

type noChanges struct{}

func (noChanges) Error() string { return "metadata images: no changes to apply" }

var errNoChanges = noChanges{}

func Apply(ctx context.Context, hc *http.Client, local imagetree.Staged, opts Opts) (*Result, error) {
	if !opts.DryRun && !opts.Confirm {
		return nil, confirmRequired()
	}
	if opts.Dir == "" {
		return nil, exit.Usagef("internal: metadata images apply called without the scanned directory")
	}

	locales := filterLocales(local, opts.Locales)
	types := filterTypes(opts.Types)

	if !opts.NoValidate {
		problems, err := imagevalidate.Validate(scopedTree(local, locales, types))
		if err != nil {
			return nil, err
		}
		if len(problems) > 0 {
			return nil, &ValidationError{Problems: problems}
		}
	}

	result := &Result{Package: opts.Package, DryRun: opts.DryRun}

	if opts.DryRun {
		if err := edits.WithReadOnlyEdit(ctx, hc, opts.Package, func(editID string) error {
			plans, _, err := planSlots(ctx, hc, opts.Package, editID, opts.Dir, local, locales, types, opts.Prune)
			if err != nil {
				return err
			}
			result.Diff = imagediff.Aggregate(opts.Package, plans)
			return nil
		}); err != nil {
			return nil, err
		}
		return result, nil
	}

	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		plans, inputs, err := planSlots(ctx, hc, opts.Package, editID, opts.Dir, local, locales, types, opts.Prune)
		if err != nil {
			return err
		}
		result.Diff = imagediff.Aggregate(opts.Package, plans)
		if !result.Diff.HasChanges() {
			return errNoChanges
		}
		for i, p := range plans {
			if err := executePlan(ctx, hc, opts.Package, editID, opts.Dir, local[p.Locale][p.Type], inputs[i].Local, p, !opts.NoValidate, opts.AIGenerated); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoChanges) {
		return nil, err
	}
	return result, nil
}

func planSlots(ctx context.Context, hc *http.Client, pkg, editID, dir string, local imagetree.Staged, locales []string, types []images.Type, prune bool) ([]imagediff.Plan, []imagediff.SlotInput, error) {
	inputs := make([]imagediff.SlotInput, 0, len(locales)*len(types))
	for _, loc := range locales {
		for _, ty := range types {
			inputs = append(inputs, imagediff.SlotInput{Locale: loc, Type: ty})
		}
	}
	err := fanout.Each(len(inputs), func(i int) error {
		in := &inputs[i]
		live, _, err := images.List(ctx, hc, pkg, editID, in.Locale, in.Type)
		if err != nil {
			return &LocaleError{Locale: in.Locale, Err: err}
		}
		shas, err := hashFiles(dir, local[in.Locale][in.Type])
		if err != nil {
			return err
		}
		in.Live, in.Local = live, shas
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return imagediff.Plans(inputs, prune), inputs, nil
}

func hashFiles(dir string, paths []string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		f, err := imagetree.Open(dir, p)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		out[i] = hex.EncodeToString(h.Sum(nil))
	}
	return out, nil
}

func executePlan(ctx context.Context, hc *http.Client, pkg, editID, dir string, files, shas []string, p imagediff.Plan, validate bool, ai images.AIGeneratedState) error {
	switch p.Kind {
	case imagediff.KindNone:
		return nil
	case imagediff.KindAppend:
		return uploadAll(ctx, hc, pkg, editID, dir, files, shas, p, validate, ai)
	case imagediff.KindRewrite:
		if err := images.DeleteAll(ctx, hc, pkg, editID, p.Locale, p.Type); err != nil {
			return err
		}
		return uploadAll(ctx, hc, pkg, editID, dir, files, shas, p, validate, ai)
	case imagediff.KindDeleteIDs:
		for _, id := range p.DeleteIDs {
			if err := images.Delete(ctx, hc, pkg, editID, p.Locale, p.Type, id); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

func uploadAll(ctx context.Context, hc *http.Client, pkg, editID, dir string, files, shas []string, p imagediff.Plan, validate bool, ai images.AIGeneratedState) error {
	for _, i := range p.Uploads {
		data, err := readContained(dir, files[i])
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != shas[i] {
			return &ChangedError{Path: files[i], Planned: shas[i], Got: got}
		}
		if validate {
			if problems := imagevalidate.CheckImage(p.Locale, p.Type, i, data); len(problems) > 0 {
				return &ValidationError{Problems: problems}
			}
		}
		if _, err := images.Upload(ctx, hc, pkg, editID, p.Locale, p.Type, data, ai); err != nil {
			return err
		}
	}
	return nil
}

func readContained(dir, p string) ([]byte, error) {
	f, err := imagetree.Open(dir, p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

type ChangedError struct{ Path, Planned, Got string }

func (e *ChangedError) Error() string {
	return e.Path + " changed during the apply (planned sha256 " + e.Planned + ", now " + e.Got + "): refusing to upload content other than the planned one; rerun the apply"
}
func (e *ChangedError) ExitCode() int { return 20 }

func filterLocales(local imagetree.Staged, allow []string) []string {
	allowSet := toSet(allow)
	out := make([]string, 0, len(local))
	for loc := range local {
		if len(allowSet) == 0 || allowSet[loc] {
			out = append(out, loc)
		}
	}
	sort.Strings(out)
	return out
}

func filterTypes(allow []string) []images.Type {
	allowSet := toSet(allow)
	out := make([]images.Type, 0, len(images.Types()))
	for _, ty := range images.Types() {
		if len(allowSet) == 0 || allowSet[string(ty)] {
			out = append(out, ty)
		}
	}
	return out
}

func scopedTree(local imagetree.Staged, locales []string, types []images.Type) imagetree.Staged {
	tySet := make(map[images.Type]bool, len(types))
	for _, ty := range types {
		tySet[ty] = true
	}
	out := make(imagetree.Staged)
	for _, loc := range locales {
		for ty, seq := range local[loc] {
			if !tySet[ty] || len(seq) == 0 {
				continue
			}
			if out[loc] == nil {
				out[loc] = make(map[images.Type][]string)
			}
			out[loc][ty] = seq
		}
	}
	return out
}

func toSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
