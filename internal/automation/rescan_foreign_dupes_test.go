package automation

import (
	"path/filepath"
	"testing"
)

// A two-episode file recorded for one of its episodes only is still the other episode's
// only copy: the duplicate check never offers it beside a better copy of the first, unless
// the other episode has its own file, recorded and on disk.
func TestSeriesDuplicatesNeverOfferAnotherEpisodesOnlyCopy(t *testing.T) {
	for _, c := range []struct {
		name     string
		e1Own    bool // E1 has its own file, recorded and on disk
		e1Gone   bool // E1 is recorded to a file that's gone
		wantDupe bool
	}{
		{name: "E1 unrecorded"},
		{name: "E1 recorded to a gone file", e1Gone: true},
		{name: "E1 has its own file", e1Own: true, wantDupe: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newLifecycleHarness(t)
			mustExec(t, h.c, `UPDATE series SET year = 2010 WHERE id = ?`, h.seriesID)
			season := filepath.Join(h.c.imp.TVRoot(), "Show (2010)", "Season 1")
			double := filepath.Join(season, "Show - S01E01E02 - 720p.mkv")
			better := filepath.Join(season, "Show - S01E02 - 2160p.mkv")
			sparseFile(t, double, 60<<20)
			sparseFile(t, better, 50<<20)
			rvSet(t, h, 1, 2, double)
			switch {
			case c.e1Own:
				own := filepath.Join(season, "Show - S01E01 - 2160p.mkv")
				sparseFile(t, own, 90<<20)
				rvSet(t, h, 1, 1, own)
			case c.e1Gone:
				rvSet(t, h, 1, 1, filepath.Join(season, "Show - S01E01 - gone.mkv"))
			}

			dupes, err := h.c.SeriesDuplicates(h.ctx, h.seriesID)
			if err != nil {
				t.Fatal(err)
			}
			offered := false
			for _, d := range dupes {
				for _, e := range d.Extras {
					offered = offered || filepath.Clean(e.Path) == filepath.Clean(double)
				}
			}
			if offered != c.wantDupe {
				t.Errorf("two-episode file offered = %v, want %v (dupes %+v)", offered, c.wantDupe, dupes)
			}
		})
	}
}
