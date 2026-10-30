package movies

import (
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

func TestAvailable(t *testing.T) {
	now := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	rel := func(date string) *MovieExtra { return &MovieExtra{ReleaseDate: date} }

	cases := []struct {
		name string
		m    Movie
		want bool
	}{
		{"announced always searchable", Movie{MinAvailability: "announced"}, true},
		{"released + status Released", Movie{MinAvailability: "released", Status: "Released"}, true},
		{"released + status Released but future date → not yet", Movie{MinAvailability: "released", Status: "Released", Extra: rel("2026-12-25")}, false},
		{"released + future date → not yet", Movie{MinAvailability: "released", Extra: rel("2026-12-25")}, false},
		{"released + long-past date → available", Movie{MinAvailability: "released", Extra: rel("2025-01-01")}, true},
		{"released + in cinemas today → not yet (90 days)", Movie{MinAvailability: "released", Extra: rel("2026-07-16")}, false},
		{"released, no date, not Released → strict skip", Movie{MinAvailability: "released", Status: "Post Production"}, false},
		{"inCinemas + today → available", Movie{MinAvailability: "inCinemas", Extra: rel("2026-07-16")}, true},
		{"inCinemas + future date → not yet", Movie{MinAvailability: "inCinemas", Extra: rel("2026-07-17")}, false},
		{"inCinemas, no date, not Released → lenient allow", Movie{MinAvailability: "inCinemas", Status: "Post Production"}, true},
		{"empty min avail → default allow", Movie{}, true},
	}
	for _, tc := range cases {
		if got := Available(tc.m, now); got != tc.want {
			t.Errorf("%s: available = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// "Released" waits for the digital or disc release, the earlier of the two; with neither
// known, the cinema date + 90 days; with no date at all, TMDB's status.
func TestReleasedWaitsForHomeRelease(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	day := func(d string) *metadata.DatedRelease { return &metadata.DatedRelease{Date: d, Country: "US"} }
	film := func(main string, rd *metadata.ReleaseDates) Movie {
		return Movie{MinAvailability: "released", Extra: &MovieExtra{ReleaseDate: main, Releases: rd}}
	}
	cases := []struct {
		name string
		m    Movie
		want Availability
	}{
		{"digital only, past", film("2026-08-01", &metadata.ReleaseDates{Digital: day("2026-10-01")}),
			Availability{true, "2026-10-01", AvailableByDigital}},
		{"digital only, future: in cinemas isn't enough", film("2026-10-01", &metadata.ReleaseDates{Cinema: day("2026-10-01"), Digital: day("2026-12-12")}),
			Availability{false, "2026-12-12", AvailableByDigital}},
		{"digital today", film("2026-09-01", &metadata.ReleaseDates{Digital: day("2026-10-30")}),
			Availability{true, "2026-10-30", AvailableByDigital}},
		{"physical only, past", film("2026-06-01", &metadata.ReleaseDates{Physical: day("2026-09-15")}),
			Availability{true, "2026-09-15", AvailableByPhysical}},
		{"physical only, future", film("2026-09-01", &metadata.ReleaseDates{Physical: day("2026-11-20")}),
			Availability{false, "2026-11-20", AvailableByPhysical}},
		{"both: the earlier (digital) decides", film("2026-09-01", &metadata.ReleaseDates{Digital: day("2026-11-01"), Physical: day("2026-12-01")}),
			Availability{false, "2026-11-01", AvailableByDigital}},
		{"both: the earlier (disc) decides, past", film("2026-06-01", &metadata.ReleaseDates{Digital: day("2026-11-01"), Physical: day("2026-10-01")}),
			Availability{true, "2026-10-01", AvailableByPhysical}},
		{"neither: cinema + 90 days, not yet", film("2026-09-01", &metadata.ReleaseDates{Cinema: day("2026-09-01")}),
			Availability{false, "2026-11-30", AvailableByCinema}},
		{"neither: cinema + 90 days, passed", film("2026-07-01", &metadata.ReleaseDates{Cinema: day("2026-07-01")}),
			Availability{true, "2026-09-29", AvailableByCinema}},
		{"neither, dates never read: main release date + 90 days", film("2026-09-01", nil),
			Availability{false, "2026-11-30", AvailableByCinema}},
		{"neither, read and empty: main release date + 90 days, passed", film("2001-05-04", &metadata.ReleaseDates{}),
			Availability{true, "2001-08-02", AvailableByCinema}},
		{"the earlier of cinema and main date counts: cinema", film("2026-09-20", &metadata.ReleaseDates{Cinema: day("2026-09-01")}),
			Availability{false, "2026-11-30", AvailableByCinema}},
		{"the earlier of cinema and main date counts: main", film("2026-05-01", &metadata.ReleaseDates{Cinema: day("2026-09-01")}),
			Availability{true, "2026-07-30", AvailableByCinema}},
		{"a 1954 film whose region lists only a 2026 cinema re-run", film("1954-10-15", &metadata.ReleaseDates{Cinema: day("2026-09-01")}),
			Availability{true, "1955-01-13", AvailableByCinema}},
		{"a 1985 film with a 2026 re-run and a coming 4K disc", film("1985-07-03", &metadata.ReleaseDates{Cinema: day("2026-01-10"), Physical: day("2026-12-01")}),
			Availability{true, "1986-07-03", AvailableByCinema}},
		{"a re-release years later doesn't hold an old film back", film("1985-07-03", &metadata.ReleaseDates{Cinema: day("1985-07-03"), Physical: day("2027-03-01")}),
			Availability{true, "1986-07-03", AvailableByCinema}},
		{"the region's digital day is late, the US one decides", film("2026-08-01", &metadata.ReleaseDates{Region: "GB",
			Cinema: day("2026-08-01"), Digital: &metadata.DatedRelease{Date: "2027-02-17", Country: "GB"}, EarliestDigital: day("2026-08-31")}),
			Availability{true, "2026-08-31", AvailableByDigital}},
		{"an earliest disc anywhere beats the region's digital", film("2026-08-01", &metadata.ReleaseDates{Region: "GB",
			Digital: &metadata.DatedRelease{Date: "2026-12-01", Country: "GB"}, EarliestDigital: &metadata.DatedRelease{Date: "2026-12-01", Country: "GB"},
			EarliestPhysical: &metadata.DatedRelease{Date: "2026-11-15", Country: "DE"}}),
			Availability{false, "2026-11-15", AvailableByPhysical}},
		{"nothing at all, TMDB says Released", Movie{MinAvailability: "released", Status: "Released"}, Availability{Available: true}},
		{"nothing at all, not Released", Movie{MinAvailability: "released", Status: "In Production"}, Availability{}},
		{"an unreadable date counts as none", film("", &metadata.ReleaseDates{Digital: day("soon")}), Availability{}},
	}
	for _, tc := range cases {
		if got := AvailabilityOf(tc.m, now); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
	// "inCinemas" and "announced" don't wait for a home release.
	early := Movie{Extra: &MovieExtra{ReleaseDate: "2026-10-01", Releases: &metadata.ReleaseDates{Digital: day("2026-12-12")}}}
	early.MinAvailability = "inCinemas"
	if got := AvailabilityOf(early, now); got != (Availability{true, "2026-10-01", AvailableByRelease}) {
		t.Errorf("inCinemas: got %+v", got)
	}
	early.MinAvailability = "announced"
	if !Available(early, now) {
		t.Error("announced: not available")
	}
}
