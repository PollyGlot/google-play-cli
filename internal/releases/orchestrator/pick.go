package orchestrator

import (
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

type pickOutcome int

const (
	picked pickOutcome = iota
	noReleaseOnTrack
	noMatchingRelease
	severalMatchingReleases
)

type releasePick struct {
	outcome  pickOutcome
	index    int
	releases []tracks.Release
	filtered bool
}

func pickRelease(releases []tracks.Release, versionCode int, releaseName string) releasePick {
	filtered := versionCode != 0 || releaseName != ""
	if len(releases) == 0 {
		return releasePick{outcome: noReleaseOnTrack, filtered: filtered}
	}
	var matches []int
	for i, r := range releases {
		if releaseName != "" && r.Name != releaseName {
			continue
		}
		if versionCode != 0 && !containsVersionCode(r.VersionCodes, versionCode) {
			continue
		}
		matches = append(matches, i)
	}
	switch len(matches) {
	case 0:
		return releasePick{outcome: noMatchingRelease, releases: releases, filtered: filtered}
	case 1:
		return releasePick{outcome: picked, index: matches[0], filtered: filtered}
	}
	candidates := make([]tracks.Release, 0, len(matches))
	for _, i := range matches {
		candidates = append(candidates, releases[i])
	}
	return releasePick{outcome: severalMatchingReleases, releases: candidates, filtered: filtered}
}

func (p releasePick) ambiguous(track string) *AmbiguousReleaseError {
	return &AmbiguousReleaseError{Track: track, Candidates: p.releases, FilterApplied: p.filtered}
}

func containsVersionCode(codes []string, want int) bool {
	for _, c := range codes {
		got, err := strconv.Atoi(c)
		if err != nil {
			continue
		}
		if got == want {
			return true
		}
	}
	return false
}
