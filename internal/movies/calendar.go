package movies

import (
	"context"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// UpcomingMovie is a library movie with a release date, for the Calendar module.
type UpcomingMovie struct {
	ReleaseDate string `json:"release_date"`
	// Release is which date this is: "" the film's main release date, "digital" or
	// "physical" its digital or disc release (Extra.Releases).
	Release   string `json:"release,omitempty"`
	ID        int64  `json:"id"`
	TMDBID    int    `json:"tmdb_id"`
	Title     string `json:"title"` // the shown title (Settings → Library → "Show titles as")
	Year      int    `json:"year"`
	PosterURL string `json:"poster_url"`
	HasFile   bool   `json:"has_file"`
	Monitored bool   `json:"monitored"`
	// SearchStart: this is the day a monitored, missing film reaches its minimum
	// availability (AvailabilityOf), so the day searching for it starts.
	SearchStart bool `json:"search_start,omitempty"`
	// CanonicalTitle is the film's own (TMDB English) title when Title is its shown one:
	// the adult filter judges both.
	CanonicalTitle string `json:"-"`
}

// Upcoming returns library movies whose release date falls within [from, to] (inclusive,
// YYYY-MM-DD), and their digital and physical releases in the window as entries of their
// own. A digital or physical date on the main date's day is left out: the film is already
// on that day. Release dates live in the Extra JSON, so we filter the list in memory.
func (s *Service) Upcoming(ctx context.Context, from, to string) ([]UpcomingMovie, error) {
	ms, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []UpcomingMovie
	now := time.Now()
	for _, m := range ms {
		if m.Extra == nil {
			continue
		}
		start := ""
		if m.Monitored && !m.HasFile {
			start = AvailabilityOf(m, now).From
		}
		entry := func(rd, release string) UpcomingMovie {
			return UpcomingMovie{
				ReleaseDate: rd, Release: release, ID: m.ID, TMDBID: m.TMDBID, Title: m.Shown(), Year: m.Year,
				PosterURL: m.PosterURL, HasFile: m.HasFile, Monitored: m.Monitored, CanonicalTitle: m.Title,
				SearchStart: start != "" && rd == start,
			}
		}
		main := dayOf(m.Extra.ReleaseDate)
		if main != "" && main >= from && main <= to {
			out = append(out, entry(main, ""))
		}
		if r := m.Extra.Releases; r != nil {
			for _, k := range []struct {
				rel  *metadata.DatedRelease
				name string
			}{{r.Digital, "digital"}, {r.Physical, "physical"}} {
				if k.rel == nil {
					continue
				}
				if d := dayOf(k.rel.Date); d != "" && d != main && d >= from && d <= to {
					out = append(out, entry(d, k.name))
				}
			}
		}
	}
	return out, nil
}

// dayOf is a stored date's YYYY-MM-DD, guarding against a time component; "" when it is
// too short to be one.
func dayOf(s string) string {
	if len(s) < 10 {
		return ""
	}
	return s[:10]
}
