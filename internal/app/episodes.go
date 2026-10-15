package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/release"
	"github.com/laedeli/acquire/internal/store"
)

// Mapping a finished download's files to the targets its grab covers.
//
// A season pack is one download and many files; a file can hold several
// episodes in a row. Each file's name says which episodes it holds
// (release.ParseCoverage); the file is handed to the catalog for exactly
// those, and the grab's targets it holds are answered by that hand-over. A
// file that names no target of the grab is left out, with a note, and a
// target no file holds fails.

// videoFile is one video in a finished download. Size is -1 when the file
// cannot be read (a path the client reported that is not mounted here).
type videoFile struct {
	Path string
	Size int64
}

// fileFor is one file to hand over and the grab's targets it holds, in
// episode order.
type fileFor struct {
	File     videoFile
	Coverage release.Coverage
	Targets  []store.Target
}

// episodePlan is what a finished download's files are handed over for.
type episodePlan struct {
	Files []fileFor
	// Left are the files left out, each with why.
	Left []string
	// Missing are the grab's targets no file holds.
	Missing []store.Target
}

var (
	// sampleRE finds a release's sample by its file name.
	sampleRE = regexp.MustCompile(`(?i)(^|[^a-z])sample([^a-z]|$)`)
	// extrasDirRE finds the folders samples and extras sit in. Only whole
	// folder names: words such as "trailer" are in shows' titles too.
	extrasDirRE = regexp.MustCompile(`(?i)^(samples?|extras?|featurettes?|trailers?|bonus|behind[ ._-]the[ ._-]scenes|deleted[ ._-]scenes)$`)
)

// isExtra reports whether a video is a sample or an extra, by its name or the
// folder it is in.
func isExtra(path string) bool {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return sampleRE.MatchString(base) || extrasDirRE.MatchString(filepath.Base(filepath.Dir(path)))
}

// listVideos lists the video files of a finished download — the paths it
// reported and the files in any of them that is a folder — without samples
// and extras, in path order.
func listVideos(paths []string) []videoFile {
	seen := map[string]bool{}
	var out []videoFile
	add := func(p string, size int64) {
		if seen[p] || !katalog.IsVideo(p) || isExtra(p) {
			return
		}
		seen[p] = true
		out = append(out, videoFile{Path: p, Size: size})
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			// Reported by the client but not readable here: kept by its name,
			// as ResolveVideo keeps one, so the hand-over can say what failed.
			add(p, -1)
			continue
		}
		if !info.IsDir() {
			add(p, info.Size())
			continue
		}
		_ = filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if fi, err := d.Info(); err == nil {
				add(fp, fi.Size())
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// fileCoverage reads the episodes a file holds from its name, or — a file
// named after nothing in a folder named after its episode, as obfuscated
// downloads are — from its folder's name. Whole seasons are no answer for a
// file.
func fileCoverage(path string) release.Coverage {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if c := release.ParseCoverage(base); c.Found && !c.Pack() {
		return c
	}
	if c := release.ParseCoverage(filepath.Base(filepath.Dir(path))); c.Found && !c.Pack() {
		return c
	}
	return release.Coverage{}
}

// planEpisodes maps the files of a series grab's download to the grab's
// targets. Each file goes for the episodes its name holds; of two files for
// the same episode the larger is taken. A download whose one file names no
// episode is the release itself, and goes for what the release's name holds.
func planEpisodes(videos []videoFile, targets []store.Target, releaseName string) episodePlan {
	var plan episodePlan
	byEpisode := map[[2]int]store.Target{}
	for _, t := range targets {
		if t.SeasonNumber != nil && t.EpisodeNumber != nil {
			byEpisode[[2]int{*t.SeasonNumber, *t.EpisodeNumber}] = t
		}
	}
	type named struct {
		file videoFile
		cov  release.Coverage
	}
	var known []named
	var unnamed []videoFile
	for _, v := range videos {
		if c := fileCoverage(v.Path); c.Found {
			known = append(known, named{v, c})
		} else {
			unnamed = append(unnamed, v)
		}
	}
	if len(known) == 0 && len(unnamed) == 1 {
		if c := release.ParseCoverage(releaseName); c.Found && !c.Pack() {
			known, unnamed = append(known, named{unnamed[0], c}), nil
		}
	}
	for _, v := range unnamed {
		plan.Left = append(plan.Left, filepath.Base(v.Path)+": no episode in its name")
	}
	sort.SliceStable(known, func(i, j int) bool {
		a, b := known[i].cov, known[j].cov
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		if a.Episode != b.Episode {
			return a.Episode < b.Episode
		}
		return known[i].file.Size > known[j].file.Size
	})
	taken := map[[2]int]bool{}
	for _, k := range known {
		var holds []store.Target
		overlaps := false
		for e := k.cov.Episode; e <= k.cov.EpisodeEnd; e++ {
			key := [2]int{k.cov.Season, e}
			overlaps = overlaps || taken[key]
			if t, ok := byEpisode[key]; ok {
				holds = append(holds, t)
			}
		}
		name := filepath.Base(k.file.Path)
		switch {
		case overlaps:
			plan.Left = append(plan.Left, name+": a second file for "+k.cov.String())
			continue
		case len(holds) == 0:
			plan.Left = append(plan.Left, name+": "+k.cov.String()+" is not one this grab is for (held already, or not wanted)")
			continue
		}
		for e := k.cov.Episode; e <= k.cov.EpisodeEnd; e++ {
			taken[[2]int{k.cov.Season, e}] = true
		}
		plan.Files = append(plan.Files, fileFor{File: k.file, Coverage: k.cov, Targets: holds})
	}
	for _, t := range targets {
		if t.SeasonNumber == nil || t.EpisodeNumber == nil || !taken[[2]int{*t.SeasonNumber, *t.EpisodeNumber}] {
			plan.Missing = append(plan.Missing, t)
		}
	}
	return plan
}

// planMovie hands a movie grab's largest video over for its target.
func planMovie(videos []videoFile, targets []store.Target) episodePlan {
	var plan episodePlan
	if len(videos) == 0 || len(targets) == 0 {
		plan.Missing = targets
		return plan
	}
	best := videos[0]
	for _, v := range videos[1:] {
		if v.Size > best.Size {
			best = v
		}
	}
	plan.Files = []fileFor{{File: best, Targets: targets[:1]}}
	return plan
}
