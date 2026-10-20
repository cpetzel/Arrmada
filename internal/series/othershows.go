package series

import "context"

// OtherShowsIn is up to limit OTHER series with an episode file inside the folder dir —
// Repo.OtherSeriesUnder, for callers outside this package (the rename guard, the
// year-when-needed check): a whole-path-prefix match, never a LIKE pattern, and a read
// error answers as shared so the caller refuses or keeps the year.
func (s *Service) OtherShowsIn(ctx context.Context, seriesID int64, dir string, limit int) []int64 {
	if dir == "" {
		return nil
	}
	if limit <= 0 {
		limit = 1
	}
	return s.repo.OtherSeriesUnder(ctx, seriesID, dir, limit)
}
