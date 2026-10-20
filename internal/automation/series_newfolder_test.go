package automation

import (
	"io"
	"log/slog"
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
