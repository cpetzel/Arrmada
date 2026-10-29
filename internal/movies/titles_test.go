package movies

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// titlesMeta answers GetMovie with an original and alternative titles, counting calls.
type titlesMeta struct {
	calls   atomic.Int32
	missing int // a TMDB id TMDB no longer has
}

func (f *titlesMeta) Available() bool { return true }
func (f *titlesMeta) SearchMovie(context.Context, string) ([]metadata.MovieResult, error) {
	return nil, nil
}
func (f *titlesMeta) GetMovie(_ context.Context, id int) (*metadata.MovieDetails, error) {
	f.calls.Add(1)
	if id == f.missing {
		return nil, metadata.ErrNotFound
	}
	return &metadata.MovieDetails{OriginalTitle: "Original " + itoa(id), AltTitles: []string{"Alt " + itoa(id)},
		Releases: &metadata.ReleaseDates{Digital: &metadata.DatedRelease{Date: "2026-11-0" + string(rune('0'+id%10)), Country: "US"}}}, nil
}
func (f *titlesMeta) GetCollection(context.Context, int) (*metadata.Collection, error) {
	return nil, errors.New("no collections here")
}

func itoa(n int) string {
	return string(rune('A' + n%26))
}

func titlesService(t *testing.T, meta metadata.MovieProvider) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB(), meta, nil, t.TempDir(), "", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Movies stored before titles were kept get them, at the given pace, keeping the rest of
// their extra metadata; movies already read are not asked about again.
func TestBackfillAltTitles(t *testing.T) {
	meta := &titlesMeta{missing: 3}
	s := titlesService(t, meta)
	ctx := context.Background()
	old, err := s.repo.Create(ctx, Movie{TMDBID: 1, Title: "One", Year: 2001, Extra: &MovieExtra{Genres: []string{"Drama"}}})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := s.repo.Create(ctx, Movie{TMDBID: 2, Title: "Two", Year: 2002})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := s.repo.Create(ctx, Movie{TMDBID: 3, Title: "Three", Year: 2003})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.Create(ctx, Movie{TMDBID: 4, Title: "Four", Year: 2004, Extra: &MovieExtra{TitlesRead: true, Releases: &metadata.ReleaseDates{}}}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	n, err := s.BackfillAltTitles(ctx, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || meta.calls.Load() != 3 {
		t.Fatalf("filled %d with %d TMDB calls, want 3 and 3 (the read one is skipped)", n, meta.calls.Load())
	}
	if time.Since(start) < 60*time.Millisecond {
		t.Errorf("three movies took %v: the pace wasn't kept", time.Since(start))
	}
	got, _ := s.repo.Get(ctx, old.ID)
	if got.Extra == nil || !got.Extra.TitlesRead || got.Extra.OriginalTitle != "Original B" || len(got.Extra.AltTitles) != 1 ||
		len(got.Extra.Genres) != 1 || got.Extra.Genres[0] != "Drama" {
		t.Errorf("extra = %+v, want titles added and genres kept", got.Extra)
	}
	if got, _ := s.repo.Get(ctx, bare.ID); got.Extra == nil || got.Extra.AltTitles[0] != "Alt C" {
		t.Errorf("a movie without extra gets one: %+v", got.Extra)
	}
	if got, _ := s.repo.Get(ctx, gone.ID); got.Extra == nil || !got.Extra.TitlesRead || got.Extra.OriginalTitle != "" ||
		got.Extra.Releases == nil || got.Extra.Releases.Digital != nil {
		t.Errorf("a film TMDB no longer has is marked read: %+v", got.Extra)
	}

	// A second run has nothing to do.
	if n, _ := s.BackfillAltTitles(ctx, time.Millisecond); n != 0 || meta.calls.Load() != 3 {
		t.Errorf("second run filled %d (%d calls), want nothing", n, meta.calls.Load())
	}
}

// Movies stored before release dates were kept get them from the same pass, without their
// titles being rewritten; a film with both is left alone.
func TestBackfillReleaseDates(t *testing.T) {
	meta := &titlesMeta{}
	s := titlesService(t, meta)
	ctx := context.Background()
	old, err := s.repo.Create(ctx, Movie{TMDBID: 5, Title: "Five", Year: 2005,
		Extra: &MovieExtra{TitlesRead: true, AltTitles: []string{"Kept"}, ReleaseDate: "2026-09-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.Create(ctx, Movie{TMDBID: 6, Title: "Six", Year: 2006,
		Extra: &MovieExtra{TitlesRead: true, Releases: &metadata.ReleaseDates{}}}); err != nil {
		t.Fatal(err)
	}
	n, err := s.BackfillAltTitles(ctx, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || meta.calls.Load() != 1 {
		t.Fatalf("filled %d with %d TMDB calls, want 1 and 1", n, meta.calls.Load())
	}
	got, _ := s.repo.Get(ctx, old.ID)
	if got.Extra == nil || got.Extra.Releases == nil || got.Extra.Releases.Digital == nil || got.Extra.Releases.Digital.Date != "2026-11-05" {
		t.Fatalf("releases = %+v, want the digital date read", got.Extra)
	}
	if len(got.Extra.AltTitles) != 1 || got.Extra.AltTitles[0] != "Kept" || got.Extra.ReleaseDate != "2026-09-01" {
		t.Errorf("extra = %+v, want its titles and main date kept", got.Extra)
	}
}

// New movies store their release dates straight away, read even when TMDB has none.
func TestExtraFromKeepsReleases(t *testing.T) {
	rd := &metadata.ReleaseDates{Physical: &metadata.DatedRelease{Date: "2026-12-01", Country: "AU"}}
	if ex := extraFrom(&metadata.MovieDetails{Releases: rd}); ex.Releases != rd {
		t.Errorf("releases = %+v", ex.Releases)
	}
	if ex := extraFrom(&metadata.MovieDetails{}); ex.Releases == nil {
		t.Error("no releases from TMDB: want them stored as read and empty")
	}
}

func TestBackfillAltTitlesStopsWithoutTMDB(t *testing.T) {
	s := titlesService(t, nil)
	if n, err := s.BackfillAltTitles(context.Background(), time.Millisecond); n != 0 || err != nil {
		t.Errorf("no provider: %d, %v", n, err)
	}
}

// New movies store their titles straight away.
func TestExtraFromKeepsTitles(t *testing.T) {
	ex := extraFrom(&metadata.MovieDetails{OriginalTitle: "Léon", AltTitles: []string{"The Professional"}})
	if !ex.TitlesRead || ex.OriginalTitle != "Léon" || len(ex.AltTitles) != 1 {
		t.Errorf("extraFrom = %+v", ex)
	}
}
