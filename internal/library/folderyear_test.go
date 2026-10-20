package library

import (
	"os"
	"path/filepath"
	"testing"
)

// showFolderOf is the series folder an episode target sits in (target = show/season/file).
func showFolderOf(target string) string { return filepath.Base(filepath.Dir(filepath.Dir(target))) }

// "Add the year only when it's needed" keeps {year} in a NEW show folder only when the check
// says another show shares the title; with the setting off, or no check installed, every
// folder is named exactly as before.
func TestSeriesFolderYearWhenNeeded(t *testing.T) {
	needed := map[string]bool{"Dark Matter": true} // the check's answer per title
	asked := 0
	check := func(title string, _ int) bool { asked++; return needed[title] }

	cases := []struct {
		name     string
		naming   SeriesNaming
		check    bool // install the check
		title    string
		year     int
		existing string // the show's folder, when it has one
		want     string
	}{
		{"off: the template as always", SeriesNaming{Folder: "{title} ({year})"}, true, "Breaking Bad", 2008, "", "Breaking Bad (2008)"},
		{"off: a shared title too", SeriesNaming{Folder: "{title} ({year})"}, true, "Dark Matter", 2024, "", "Dark Matter (2024)"},
		{"on: not needed drops the year and its brackets", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "", "Breaking Bad"},
		{"on: needed keeps it", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Dark Matter", 2024, "", "Dark Matter (2024)"},
		{"on: Plex ids stay", SeriesNaming{Folder: "{title} ({year}) {tvdb-{tvdbid}}", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "", "Breaking Bad {tvdb-81189}"},
		{"on: other separators tidy up", SeriesNaming{Folder: "{title} - [{year}]", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "", "Breaking Bad"},
		{"on: no check installed keeps the year", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, false, "Breaking Bad", 2008, "", "Breaking Bad (2008)"},
		{"on: a template with no {year} is untouched", SeriesNaming{Folder: "{title}", YearWhenNeeded: true}, true, "Dark Matter", 2024, "", "Dark Matter"},
		{"on: an existing folder is reused as it is", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Breaking Bad", 2008, "Breaking Bad (2008)", "Breaking Bad (2008)"},
		{"on: an existing year-less folder is reused too", SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}, true, "Dark Matter", 2024, "Dark Matter", "Dark Matter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			im := NewImporter("/tv", quiet())
			im.SetSeriesNaming(fixedSeriesNaming{c.naming})
			im.SetSeriesIDsFunc(func(string, int) SeriesIDs { return SeriesIDs{TMDB: 1396, TVDB: 81189} })
			if c.check {
				im.SetSeriesYearNeededFunc(check)
			}
			got := im.EpisodeTargetIn(c.existing, c.title, c.year, 1, 1, "x.S01E01.1080p.WEB-DL.mkv", ".mkv")
			if f := showFolderOf(got); f != c.want {
				t.Errorf("folder %q, want %q", f, c.want)
			}
		})
	}

	// The check is only asked when it can change something: the setting on, a {year} in
	// the template, and no folder yet.
	asked = 0
	im := NewImporter("/tv", quiet())
	im.SetSeriesYearNeededFunc(check)
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})"}})
	im.EpisodeTargetIn("", "Breaking Bad", 2008, 1, 1, "x.mkv", ".mkv")
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title}", YearWhenNeeded: true}})
	im.EpisodeTargetIn("", "Breaking Bad", 2008, 1, 1, "x.mkv", ".mkv")
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}})
	im.EpisodeTargetIn("Breaking Bad (2008)", "Breaking Bad", 2008, 1, 1, "x.mkv", ".mkv")
	if asked != 0 {
		t.Errorf("the check was asked %d times where it couldn't change the folder", asked)
	}
}

// A real import of a new show: with the setting on, "Breaking Bad" lands in a year-less
// folder and "Dark Matter (2024)" keeps its year, because the check says another Dark
// Matter exists. The episode file's own {year} is never touched.
func TestImportNewShowFolderYearWhenNeeded(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	im := NewImporter(root, quiet())
	im.SetSeriesNaming(fixedSeriesNaming{SeriesNaming{Folder: "{title} ({year})", EpisodeFile: "{title} ({year}) - {episode}", YearWhenNeeded: true}})
	im.SetSeriesYearNeededFunc(func(title string, _ int) bool { return title == "Dark Matter" })

	for _, c := range []struct {
		title    string
		year     int
		file     string
		wantPath string
	}{
		{"Breaking Bad", 2008, "Breaking.Bad.S01E01.1080p.WEB-DL.mkv", filepath.Join("Breaking Bad", "Season 1", "Breaking Bad (2008) - S01E01.mkv")},
		{"Dark Matter", 2024, "Dark.Matter.2024.S01E01.1080p.WEB-DL.mkv", filepath.Join("Dark Matter (2024)", "Season 1", "Dark Matter (2024) - S01E01.mkv")},
	} {
		p := filepath.Join(src, c.file)
		if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
		ei, ok, err := im.ImportEpisodeInto("", c.title, c.year, p)
		if err != nil || !ok {
			t.Fatalf("%s: import ok=%v err=%v", c.title, ok, err)
		}
		if want := filepath.Join(root, c.wantPath); ei.TargetPath != want {
			t.Errorf("%s: imported to %q, want %q", c.title, ei.TargetPath, want)
		}
	}
}
