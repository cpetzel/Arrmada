package series

import (
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// A scanned folder with no year whose title is exactly more than one show's is never
// matched to one of them — it goes to review with the candidates. A year, or a single
// exact match, still matches as before.
func TestBestSeriesMatchYearlessAmbiguous(t *testing.T) {
	both := []metadata.SeriesResult{
		{TMDBID: 200, Title: "Dark Matter", Year: 2024},
		{TMDBID: 100, Title: "Dark Matter", Year: 2015},
		{TMDBID: 300, Title: "Dark Matters", Year: 2011},
	}
	if m, ok := bestSeriesMatch(both, "Dark Matter", 0); ok {
		t.Errorf("year-less \"Dark Matter\" was matched to %d (%d); want it sent to review", m.TMDBID, m.Year)
	}
	if m, ok := bestSeriesMatch(both, "dark matter", 0); ok {
		t.Errorf("other case matched to %d", m.TMDBID)
	}
	if m, ok := bestSeriesMatch(both, "Dark Matter", 2015); !ok || m.TMDBID != 100 {
		t.Errorf("\"Dark Matter (2015)\" = %d, %v; want 100", m.TMDBID, ok)
	}
	if m, ok := bestSeriesMatch(both, "Dark Matters", 0); !ok || m.TMDBID != 300 {
		t.Errorf("a single exact match = %d, %v; want 300", m.TMDBID, ok)
	}
	one := []metadata.SeriesResult{{TMDBID: 1396, Title: "Breaking Bad", Year: 2008}, {TMDBID: 1396, Title: "Breaking Bad", Year: 2008}}
	if m, ok := bestSeriesMatch(one, "Breaking Bad", 0); !ok || m.TMDBID != 1396 {
		t.Errorf("the same show listed twice = %d, %v; want it matched", m.TMDBID, ok)
	}
}
