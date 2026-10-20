package automation

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// A placement uses the show's own folder untouched — the year check isn't even asked —
// and names a new one once, with the year only when it's needed.
func TestPlacementFolder(t *testing.T) {
	imp := library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	imp.SetSeriesNaming(yearWhenNeededNaming{})
	asked := 0
	shared := map[string]bool{"Dark Matter": true}
	imp.SetSeriesYearNeededFunc(func(title string, _ int, _ string) bool { asked++; return shared[title] })

	if got := placementFolder(imp, "Breaking Bad (2008)", series.Series{Title: "Breaking Bad", Year: 2008}); got != "Breaking Bad (2008)" || asked != 0 {
		t.Errorf("existing folder: %q, check asked %d times", got, asked)
	}
	if got := placementFolder(imp, "", series.Series{Title: "Breaking Bad", Year: 2008}); got != "Breaking Bad" || asked != 1 {
		t.Errorf("new show: %q, check asked %d times", got, asked)
	}
	if got := placementFolder(imp, "", series.Series{Title: "Dark Matter", Year: 2024}); got != "Dark Matter (2024)" {
		t.Errorf("new shared title: %q", got)
	}
}

// A placement that writes names a new show's folder once and checks that same name; a
// reader (rescan, duplicates, rename) still sees "" for a show with no files, so it reads
// only what main's proven-own rule allows. The importer's SeriesFolderPath("") gives the
// same name the writer uses.
func TestImportPlacementNamesTheFolderOnce(t *testing.T) {
	l := newFolderLib(t) // the year check says no show needs its year
	id := l.show(t, 300, "Breaking Bad", 2008, 1)
	s, err := l.c.series.Get(l.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	imp, folder, err := l.c.seriesImportPlacement(l.ctx, s)
	if err != nil || folder != "Breaking Bad" {
		t.Fatalf("import placement = %q, %v; want the year-less new folder", folder, err)
	}
	if got := imp.SeriesFolderPath("", s.Title, s.Year); got != filepath.Join(imp.TVRoot(), folder) {
		t.Errorf("SeriesFolderPath(\"\") = %q, want the placement's %q", got, folder)
	}
	if _, rf, err := l.c.seriesPlacement(l.ctx, s); err != nil || rf != "" {
		t.Errorf("reader placement = %q, %v; want \"\" for a show with no files", rf, err)
	}

	// Once the show has files, both use its folder, and the year check isn't consulted.
	l.file(t, id, 1, "Breaking Bad (2008)/Season 1/Breaking Bad - S01E01.mkv")
	if _, folder, err := l.c.seriesImportPlacement(l.ctx, s); err != nil || folder != "Breaking Bad (2008)" {
		t.Errorf("with files: import placement = %q, %v", folder, err)
	}
}
