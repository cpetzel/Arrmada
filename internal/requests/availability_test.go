package requests

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// An approval's search waits for the film's minimum availability like the scheduled one:
// a film in cinemas but not yet out on digital is approved and monitored, not searched
// (the sweep searches it from its digital release). One already out on digital is.
func TestApprovalSearchWaitsForTheDigitalRelease(t *testing.T) {
	day := func(offset int) *metadata.DatedRelease {
		return &metadata.DatedRelease{Date: time.Now().AddDate(0, 0, offset).Format(time.DateOnly), Country: "US"}
	}
	film := func(tmdb int, digital int) movies.Movie {
		return movies.Movie{TMDBID: tmdb, Title: "film", Status: "Released", MinAvailability: "released",
			Extra: &movies.MovieExtra{ReleaseDate: day(-20).Date, Releases: &metadata.ReleaseDates{Cinema: day(-20), Digital: day(digital)}}}
	}
	s, _, rec := quietFixture(t, film(901, 30), film(902, -1))
	ctx := context.Background()

	got, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 901, Title: "Not On Digital", RequestedBy: 7}, CreateOptions{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if len(rec.specs) != 0 {
		t.Fatalf("an approval searched a film not out on digital yet: %+v", rec.specs)
	}
	if m, _ := s.movies.GetByTMDB(ctx, 901); !m.Monitored {
		t.Fatal("the approved film isn't monitored, so the sweep would never search it")
	}
	// Its card says when: "Out <its digital day> — we'll grab it then".
	reqs := []Request{got}
	s.enrichAvailability(ctx, reqs)
	if reqs[0].released || reqs[0].releaseDate != day(30).Date {
		t.Fatalf("request card: released %v, out on %q; want not out, %s", reqs[0].released, reqs[0].releaseDate, day(30).Date)
	}

	if _, _, err := s.Create(ctx, Request{MediaType: "movie", TMDBID: 902, Title: "On Digital", RequestedBy: 7}, CreateOptions{AutoApprove: true}); err != nil {
		t.Fatal(err)
	}
	if len(rec.specs) != 1 || rec.specs[0].Kind != "movie.search" {
		t.Fatalf("searches started = %+v, want one movie.search for the film out on digital", rec.specs)
	}
}
