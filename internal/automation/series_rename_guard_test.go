package automation

import (
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// The merge guard reads "crossing show folders" from the paths under the TV folder, never
// from season-folder names: a rename between "Season 1" and an "S01", "Staffel 1" or
// "Saison 1" season folder stays inside the show, and isn't refused because other shows
// have season folders of that name. Before, "S01" was read as a show folder and every
// other show with an S01 made the rename "another show's folder".
func TestRenameGuardSeasonFolderSpellings(t *testing.T) {
	l := newFolderLib(t)
	other := l.show(t, 100, "Other", 2010, 3)
	l.file(t, other, 1, "Other/S01/Other - S01E01.mkv")
	l.file(t, other, 2, "Other/Staffel 1/Other - S01E02.mkv")
	l.file(t, other, 3, "Other/Saison 1/Other - S01E03.mkv")
	id := l.show(t, 200, "Show", 2020, 3)
	at := func(rel string) string { return filepath.Join(l.root, filepath.FromSlash(rel)) }

	for _, c := range []struct{ name, from, to string }{
		{"Season 1 → S01", "Show/Season 1/show.s01e01.mkv", "Show/S01/Show - S01E01.mkv"},
		{"S01 → Staffel 1", "Show/S01/show.s01e01.mkv", "Show/Staffel 1/Show - S01E01.mkv"},
		{"Staffel 1 → Saison 1", "Show/Staffel 1/show.s01e01.mkv", "Show/Saison 1/Show - S01E01.mkv"},
		{"inside S01", "Show/S01/show.s01e01.mkv", "Show/S01/Show - S01E01.mkv"},
		{"flat → Saison 1", "Show/show.s01e01.mkv", "Show/Saison 1/Show - S01E01.mkv"},
	} {
		rows := []renameRow{{Season: 1, Episode: 1, Path: at(c.from), Target: at(c.to)}}
		out, skipped := l.c.holdOtherShowsFolders(l.ctx, id, l.root, rows)
		if len(skipped) != 0 || out[0].Target != at(c.to) {
			t.Errorf("%s: refused (%+v); a rename inside one show folder must go ahead", c.name, skipped)
		}
	}

	// Into another show's folder is still refused, whatever its season folders are called.
	rows := []renameRow{{Season: 1, Episode: 1, Path: at("Show/S01/a.mkv"), Target: at("Other/S01/Show - S01E01.mkv")}}
	out, skipped := l.c.holdOtherShowsFolders(l.ctx, id, l.root, rows)
	if len(skipped) != 1 || skipped[0].Reason != reasonOtherShowsFolder || out[0].Target != out[0].Path {
		t.Errorf("a move into Other's folder: skipped %+v, target %q", skipped, out[0].Target)
	}
}

// A folder name with "_" or "%" is compared as itself: "Show_1" isn't "ShowA1", as a LIKE
// pattern would have had it.
func TestRenameGuardFolderNameIsNotAPattern(t *testing.T) {
	l := newFolderLib(t)
	other := l.show(t, 100, "ShowA1", 2010, 1)
	l.file(t, other, 1, "ShowA1/Season 1/ShowA1 - S01E01.mkv")
	pct := l.show(t, 101, "100% Show", 2011, 1)
	l.file(t, pct, 1, "100X Show/Season 1/x.mkv")
	id := l.show(t, 200, "Show 1", 2020, 2)
	at := func(rel string) string { return filepath.Join(l.root, filepath.FromSlash(rel)) }
	rows := []renameRow{
		{Season: 1, Episode: 1, Path: at("Elsewhere/Season 1/a.mkv"), Target: at("Show_1/Season 1/Show 1 - S01E01.mkv")},
		{Season: 1, Episode: 2, Path: at("Elsewhere/Season 1/b.mkv"), Target: at("100% Show/Season 1/Show 1 - S01E02.mkv")},
	}
	if _, skipped := l.c.holdOtherShowsFolders(l.ctx, id, l.root, rows); len(skipped) != 0 {
		t.Errorf("refused %+v: another show's files aren't in either folder", skipped)
	}
	if got := l.c.series.OtherShowsIn(l.ctx, id, at("ShowA1"), 5); len(got) != 1 || got[0] != other {
		t.Errorf("OtherShowsIn(ShowA1) = %v, want [%d]", got, other)
	}
	if got := l.c.series.OtherShowsIn(l.ctx, other, at("ShowA1"), 5); len(got) != 0 {
		t.Errorf("a show's own files counted as another show's: %v", got)
	}
}

// The rename after a renumber runs unattended through the same guard: with an "S01"
// season folder scheme it renames inside the show and refuses nothing.
func TestRenameAfterRenumberWithS01FoldersNotRefused(t *testing.T) {
	l := newFolderLib(t)
	l.c.imp.SetSeriesNaming(fixedSeries{"{title} ({year})", "S{season00}"})
	other := l.show(t, 100, "Other", 2010, 1)
	l.file(t, other, 1, "Other/S01/Other - S01E01.mkv")
	id := l.show(t, 200, "Show", 2020, 1)
	from := l.file(t, id, 1, "Show (2020)/Season 1/show.s01e01.mkv")
	items, err := l.c.seriesRenamePreview(l.ctx, id, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("preview = %+v, %v", items, err)
	}
	if want := filepath.Join(l.root, "Show (2020)", "S01"); items[0].Conflict != "" || items[0].From != from || filepath.Dir(items[0].To) != want {
		t.Errorf("preview item %+v; want a move into %s with no conflict", items[0], want)
	}
	res, err := l.c.seriesRename(l.ctx, id, items, false)
	if err != nil || res.Moved != 1 || len(res.Skipped) != 0 {
		t.Errorf("rename = %+v, %v", res, err)
	}
}

// fixedSeries is a series scheme with the given show and season folder templates.
type fixedSeries struct{ folder, season string }

func (f fixedSeries) SeriesNaming() library.SeriesNaming {
	return library.SeriesNaming{Folder: f.folder, SeasonFolder: f.season}
}
