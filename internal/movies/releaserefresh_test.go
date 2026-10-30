package movies

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// digitalMeta answers GetMovie with a digital release yesterday, counting calls.
type digitalMeta struct{ calls atomic.Int32 }

func (f *digitalMeta) Available() bool { return true }
func (f *digitalMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (f *digitalMeta) GetMovie(context.Context, int) (*metadata.MovieDetails, error) {
	f.calls.Add(1)
	return &metadata.MovieDetails{OriginalTitle: "changed", Releases: &metadata.ReleaseDates{
		Digital: &metadata.DatedRelease{Date: time.Now().AddDate(0, 0, -1).Format(time.DateOnly), Country: "US"}}}, nil
}
func (f *digitalMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("no collections here")
}

// A film requested before TMDB knew its digital date has its dates read again, and is
// out once the digital date TMDB now gives has passed — not at cinema + 90 days. Films not
// waiting (on disk, already out, or not on "Released") aren't asked about, and nothing of
// the film but its release dates changes.
func TestRefreshPendingReleases(t *testing.T) {
	meta := &digitalMeta{}
	s := titlesService(t, meta)
	ctx := context.Background()
	recent := time.Now().AddDate(0, 0, -30).Format(time.DateOnly)
	create := func(m Movie) Movie {
		t.Helper()
		got, err := s.repo.Create(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	waiting := create(Movie{TMDBID: 1, Title: "Waiting", Monitored: true, MinAvailability: "released",
		Extra: &MovieExtra{ReleaseDate: recent, Genres: []string{"Drama"}, Releases: &metadata.ReleaseDates{}}})
	create(Movie{TMDBID: 2, Title: "Old", Monitored: true, MinAvailability: "released",
		Extra: &MovieExtra{ReleaseDate: "2001-05-04", Releases: &metadata.ReleaseDates{}}})
	create(Movie{TMDBID: 3, Title: "Cinemas", Monitored: true, MinAvailability: "inCinemas",
		Extra: &MovieExtra{ReleaseDate: recent, Releases: &metadata.ReleaseDates{}}})
	create(Movie{TMDBID: 4, Title: "Unwanted", Monitored: false, MinAvailability: "released",
		Extra: &MovieExtra{ReleaseDate: recent, Releases: &metadata.ReleaseDates{}}})

	if before, _ := s.repo.Get(ctx, waiting.ID); Available(before, time.Now()) {
		t.Fatal("fixture: the waiting film is already out")
	}
	n, err := s.RefreshPendingReleases(ctx, time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || meta.calls.Load() != 1 {
		t.Fatalf("read %d films with %d TMDB calls, want only the waiting one", n, meta.calls.Load())
	}
	after, err := s.repo.Get(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if av := AvailabilityOf(after, time.Now()); !av.Available || av.By != AvailableByDigital {
		t.Fatalf("after the refresh: %+v, want out on digital", av)
	}
	if after.Title != "Waiting" || after.Extra == nil || len(after.Extra.Genres) != 1 || after.Extra.ReleaseDate != recent {
		t.Fatalf("the refresh changed more than the release dates: %+v %+v", after, after.Extra)
	}

	// Out now: the next run asks TMDB nothing.
	if n, err := s.RefreshPendingReleases(ctx, time.Millisecond, 10); err != nil || n != 0 || meta.calls.Load() != 1 {
		t.Fatalf("second run read %d (%v), %d calls", n, err, meta.calls.Load())
	}
}
