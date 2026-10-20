package library

import (
	"os"
	"path/filepath"
	"testing"
)

// showFolderOf is the series folder an episode target sits in (target = show/season/file).
func showFolderOf(target string) string { return filepath.Base(filepath.Dir(filepath.Dir(target))) }

// NewSeriesFolderName is the one place a new show folder is named. "Add the year only when
// it's needed" keeps {year} only when the check says another show shares the title; with
// the setting off, or no check installed, every folder is named exactly as before.
func TestNewSeriesFolderName(t *testing.T) {
	needed := map[string]bool{"Dark Matter": true} // the check's answer per title
	var gotYearless []string
	check := func(title string, _ int, yearless string) bool {
		gotYearless = append(gotYearless, yearless)
		return needed[title]
	}

	cases := []struct {
		name   string
		naming SeriesNaming
		check  bool // install the check
		title  string
		year   int
		want   string
	}{
		{"off: the template as always", SeriesNaming{Folder: "{title} ({year})"}, true, "Breaking Bad", 2008, "Breaking Bad (2008)"},
		{"off: a shared title too", SeriesNaming{Folder: "{title} ({year})"}, true, "Dark Matter", 2024, "Dark Matter (2024)"},
		{"on: not needed drops the year and its brackets", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "Breaking Bad"},
		{"on: needed keeps it", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Dark Matter", 2024, "Dark Matter (2024)"},
		{"on: Plex ids stay", SeriesNaming{Folder: "{title} ({year}) {tvdb-{tvdbid}}", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "Breaking Bad {tvdb-81189}"},
		{"on: other separators tidy up", SeriesNaming{Folder: "{title} - [{year}]", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "Breaking Bad"},
		{"on: no check installed keeps the year", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, false, "Breaking Bad", 2008, "Breaking Bad (2008)"},
		{"on: no year known", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Breaking Bad", 0, "Breaking Bad"},
		{"on: a template with no {year} is untouched", SeriesNaming{Folder: "{title}", YearWhenNeeded: true}, true, "Dark Matter", 2024, "Dark Matter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			im := NewImporter("/tv", quiet())
			im.SetSeriesNaming(fixedSeriesNaming{c.naming})
			im.SetSeriesIDsFunc(func(string, int) SeriesIDs { return SeriesIDs{TMDB: 1396, TVDB: 81189} })
			if c.check {
				im.SetSeriesYearNeededFunc(check)
			}
			if got := im.NewSeriesFolderName(c.title, c.year); got != c.want {
				t.Errorf("folder %q, want %q", got, c.want)
			}
		})
	}

	// The check is told the exact year-less name, and isn't asked when it can't matter.
	gotYearless = nil
	im := NewImporter("/tv", quiet())
	im.SetSeriesYearNeededFunc(check)
	im.SetSeriesIDsFunc(func(string, int) SeriesIDs { return SeriesIDs{TMDB: 1396, TVDB: 81189} })
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year}) {tvdb-{tvdbid}}", YearWhenNeeded: true}})
	im.NewSeriesFolderName("Breaking Bad", 2008)
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})"}})
	im.NewSeriesFolderName("Breaking Bad", 2008)
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title}", YearWhenNeeded: true}})
	im.NewSeriesFolderName("Breaking Bad", 2008)
	if len(gotYearless) != 1 || gotYearless[0] != "Breaking Bad {tvdb-81189}" {
		t.Errorf("the check was asked with %q, want once with the year-less name", gotYearless)
	}
}

// The episode target never decides the year itself: a target with no folder given keeps
// the year (only a scene-named Import gets there), and a given folder — the one a
// placement named once with NewSeriesFolderName — is used as it is.
func TestEpisodeTargetNeverDecidesTheYear(t *testing.T) {
	asked := 0
	im := NewImporter("/tv", quiet())
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}})
	im.SetSeriesYearNeededFunc(func(string, int, string) bool { asked++; return false })
	if f := showFolderOf(im.EpisodeTargetIn("", "Breaking Bad", 2008, 1, 1, "x.S01E01.mkv", ".mkv")); f != "Breaking Bad (2008)" {
		t.Errorf("no folder given: %q, want the year kept", f)
	}
	for _, existing := range []string{"Breaking Bad", "Breaking Bad (2008)"} {
		if f := showFolderOf(im.EpisodeTargetIn(existing, "Breaking Bad", 2008, 1, 1, "x.S01E01.mkv", ".mkv")); f != existing {
			t.Errorf("folder %q became %q", existing, f)
		}
	}
	if asked != 0 {
		t.Errorf("the year check was asked %d times by episode targets", asked)
	}
}

// A real import of two new shows, each placed the way a download is: the folder named once
// and passed to every file. "Breaking Bad" lands in a year-less folder and "Dark Matter
// (2024)" keeps its year; the episode file's own {year} is never touched. A second episode
// joins the first even when the check would now answer the other way.
func TestImportNewShowFolderYearWhenNeeded(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	im := NewImporter(root, quiet())
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})", EpisodeFile: "{title} ({year}) - {episode}", YearWhenNeeded: true}})
	sharedTitles := map[string]bool{"Dark Matter": true}
	im.SetSeriesYearNeededFunc(func(title string, _ int, _ string) bool { return sharedTitles[title] })

	place := func(title string, year int, folder, file string) string {
		t.Helper()
		p := filepath.Join(src, file)
		if err := os.WriteFile(p, []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		ei, ok, err := im.ImportEpisodeInto(folder, title, year, p)
		if err != nil || !ok {
			t.Fatalf("%s: import ok=%v err=%v", file, ok, err)
		}
		rel, _ := filepath.Rel(root, ei.TargetPath)
		return rel
	}
	bb := im.NewSeriesFolderName("Breaking Bad", 2008)
	if got, want := place("Breaking Bad", 2008, bb, "Breaking.Bad.S01E01.mkv"), filepath.Join("Breaking Bad", "Season 1", "Breaking Bad (2008) - S01E01.mkv"); got != want {
		t.Errorf("Breaking Bad E01 at %q, want %q", got, want)
	}
	dm := im.NewSeriesFolderName("Dark Matter", 2024)
	if got, want := place("Dark Matter", 2024, dm, "Dark.Matter.2024.S01E01.mkv"), filepath.Join("Dark Matter (2024)", "Season 1", "Dark Matter (2024) - S01E01.mkv"); got != want {
		t.Errorf("Dark Matter E01 at %q, want %q", got, want)
	}
	// Mid-pack, a namesake appears: the pack's folder was named once, so E02 still joins E01.
	sharedTitles["Breaking Bad"] = true
	if got := place("Breaking Bad", 2008, bb, "Breaking.Bad.S01E02.mkv"); showFolderOf(filepath.Join(root, got)) != "Breaking Bad" {
		t.Errorf("Breaking Bad E02 split off to %q", got)
	}
}
