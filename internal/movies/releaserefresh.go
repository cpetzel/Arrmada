package movies

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// RefreshPendingReleases reads again the release dates of the films still waiting for
// their minimum availability (releaseWaiting), one every `every`, at most limit a run
// (nearest availability day first, no day last), and returns how many it read. A film is requested before TMDB knows its digital date; without
// this its stored dates never change, and it would wait for cinema + 90 days even when it
// came out on digital at day 30. Only $.releases is written (SetReleases), so nothing
// else of the film moves. It stops when TMDB isn't configured or ctx ends; a film TMDB
// can't answer right now is left for the next run.
//
// The scheduler runs it daily: a few dozen requests a day for a household's upcoming films,
// paced as the titles backfill is.
func (s *Service) RefreshPendingReleases(ctx context.Context, every time.Duration, limit int) (int, error) {
	if s.meta == nil || !s.meta.Available() {
		return 0, nil
	}
	all, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	region, _ := s.releaseRegion()
	now := time.Now()
	// The films nearest their day first (the ones a newly known digital date can bring
	// forward soonest), those with no date last, so a long wait list never starves them.
	type waitingFilm struct {
		m    Movie
		from string
	}
	var queue []waitingFilm
	for _, m := range all {
		if releaseWaiting(m, now) {
			queue = append(queue, waitingFilm{m, AvailabilityOf(m, now).From})
		}
	}
	sort.SliceStable(queue, func(i, j int) bool {
		a, b := queue[i].from, queue[j].from
		if (a == "") != (b == "") {
			return b == ""
		}
		return a < b
	})
	if len(queue) > limit {
		queue = queue[:limit]
	}
	read := 0
	for i, w := range queue {
		m := w.m
		if i > 0 {
			t := time.NewTimer(every)
			select {
			case <-ctx.Done():
				t.Stop()
				return read, ctx.Err()
			case <-t.C:
			}
		}
		d, err := s.meta.GetMovie(ctx, m.TMDBID)
		switch {
		case errors.Is(err, metadata.ErrNotConfigured):
			return read, nil
		case errors.Is(err, metadata.ErrNotFound):
			continue // TMDB no longer has it: keep what's stored
		case err != nil:
			if ctx.Err() != nil {
				return read, ctx.Err()
			}
			s.log.Debug("movies: couldn't read release dates; trying again next run", "movie", m.Title, "err", err)
			continue
		}
		rd := d.Releases
		if rd == nil {
			rd = &metadata.ReleaseDates{Region: region}
		}
		if err := s.repo.SetReleases(ctx, m.ID, rd); err != nil {
			return read, err
		}
		read++
	}
	return read, nil
}

// releaseWaiting reports whether m's release dates are worth reading again: a monitored,
// missing film on "Released" that hasn't reached it yet. A film whose earliest cinema date
// is over a year ago never waits (AvailabilityFor), so this only ever covers recent and
// unannounced films.
func releaseWaiting(m Movie, now time.Time) bool {
	return m.TMDBID > 0 && m.Monitored && !m.HasFile && m.MinAvailability == "released" && !Available(m, now)
}
