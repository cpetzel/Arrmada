package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A film out in cinemas in the US and AU, on digital in the US only and on disc in GB
// only, with a premiere and a TV airing that never count.
const tmdbReleasesSample = `{
  "id": 693134, "title": "Dune: Part Two", "release_date": "2024-02-27",
  "release_dates": {"results": [
    {"iso_3166_1": "US", "release_dates": [
      {"type": 1, "release_date": "2024-02-15T00:00:00.000Z", "certification": "PG-13"},
      {"type": 3, "release_date": "2024-03-01T00:00:00.000Z", "certification": "PG-13"},
      {"type": 4, "release_date": "2024-04-16T00:00:00.000Z"}
    ]},
    {"iso_3166_1": "AU", "release_dates": [
      {"type": 3, "release_date": "2024-02-29T00:00:00.000Z"},
      {"type": 2, "release_date": "2024-02-28T00:00:00.000Z"},
      {"type": 6, "release_date": "2024-01-01T00:00:00.000Z"}
    ]},
    {"iso_3166_1": "GB", "release_dates": [
      {"type": 5, "release_date": "2024-05-13T00:00:00.000Z"}
    ]},
    {"iso_3166_1": "DE", "release_dates": [
      {"type": 5, "release_date": "2024-05-13T00:00:00.000Z"},
      {"type": 4, "release_date": "not a date"}
    ]}
  ]}
}`

func TestPickReleaseDates(t *testing.T) {
	day := func(r *DatedRelease) string {
		if r == nil {
			return "<none>"
		}
		return r.Date + " " + r.Country
	}
	cases := []struct {
		region                    string
		cinema, digital, physical string
	}{
		// No region: the US first, then the earliest anywhere (GB and DE tie: DE by code).
		{"", "2024-03-01 US", "2024-04-16 US", "2024-05-13 DE"},
		// The region's own dates win; a limited release counts as cinema, TV never does.
		{"AU", "2024-02-28 AU", "2024-04-16 US", "2024-05-13 DE"},
		{"GB", "2024-03-01 US", "2024-04-16 US", "2024-05-13 GB"},
		// A region with nothing falls back the same way as none.
		{"NZ", "2024-03-01 US", "2024-04-16 US", "2024-05-13 DE"},
	}
	for _, c := range cases {
		m, err := parseTMDBMovieIn([]byte(tmdbReleasesSample), c.region)
		if err != nil {
			t.Fatal(err)
		}
		r := m.Releases
		if r == nil {
			t.Fatalf("region %q: no releases", c.region)
		}
		if got := day(r.Cinema); got != c.cinema {
			t.Errorf("region %q: cinema = %s, want %s", c.region, got, c.cinema)
		}
		if got := day(r.Digital); got != c.digital {
			t.Errorf("region %q: digital = %s, want %s", c.region, got, c.digital)
		}
		if got := day(r.Physical); got != c.physical {
			t.Errorf("region %q: physical = %s, want %s", c.region, got, c.physical)
		}
	}
}

func TestPickReleaseDatesTieGoesToFirstCode(t *testing.T) {
	r := pickRelease([]tmdbReleaseDates{
		{Country: "SE", ReleaseDates: []struct {
			Certification string `json:"certification"`
			Type          int    `json:"type"`
			ReleaseDate   string `json:"release_date"`
		}{{Type: 5, ReleaseDate: "2024-05-13T00:00:00.000Z"}}},
		{Country: "fr", ReleaseDates: []struct {
			Certification string `json:"certification"`
			Type          int    `json:"type"`
			ReleaseDate   string `json:"release_date"`
		}{{Type: 5, ReleaseDate: "2024-05-13"}}},
	}, "", tmdbPhysical)
	if r == nil || r.Country != "FR" || r.Date != "2024-05-13" {
		t.Errorf("tie = %+v, want FR (codes upper-cased, first alphabetically)", r)
	}
}

// A film TMDB has no release dates for still answers, with every kind empty.
func TestPickReleaseDatesNone(t *testing.T) {
	m, err := parseTMDBMovie([]byte(tmdbMovieSample))
	if err != nil {
		t.Fatal(err)
	}
	if r := m.Releases; r == nil || r.Cinema != nil || r.Digital != nil || r.Physical != nil {
		t.Errorf("releases = %+v, want read and empty", r)
	}
}

// GetMovie and the Discover detail pick the dates for the admin's region; the detail's
// cache key names the region, so changing it doesn't serve the old region's dates.
func TestReleaseDatesFollowRegion(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.Contains(r.URL.Query().Get("append_to_response"), "release_dates") {
			t.Errorf("%s asked without release_dates", r.URL.Path)
		}
		_, _ = w.Write([]byte(tmdbReleasesSample))
	}))
	defer srv.Close()

	region := "au"
	tm := NewTMDB("k")
	tm.base = srv.URL
	tm.SetRegionFunc(func() string { return region })
	tm.SetDiskCache(testDiskCache(t))
	ctx := context.Background()

	d, err := tm.GetMovie(ctx, 693134)
	if err != nil {
		t.Fatal(err)
	}
	if d.Releases.Cinema == nil || d.Releases.Cinema.Country != "AU" {
		t.Errorf("GetMovie cinema = %+v, want AU's", d.Releases.Cinema)
	}
	md, err := tm.MediaDetails(ctx, "movie", 693134)
	if err != nil {
		t.Fatal(err)
	}
	if md.Releases == nil || md.Releases.Cinema == nil || md.Releases.Cinema.Date != "2024-02-28" {
		t.Errorf("detail cinema = %+v, want AU's 2024-02-28", md.Releases)
	}
	region = "US"
	md, err = tm.MediaDetails(ctx, "movie", 693134)
	if err != nil {
		t.Fatal(err)
	}
	if md.Releases == nil || md.Releases.Cinema == nil || md.Releases.Cinema.Country != "US" {
		t.Errorf("after the region changed, detail cinema = %+v, want the US's", md.Releases)
	}
	if calls != 3 {
		t.Errorf("TMDB calls = %d, want 3 (one per region for the detail)", calls)
	}
}
