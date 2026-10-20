package series

import (
	"context"
	"path/filepath"
)

// OtherShowsIn is up to limit OTHER series with an episode file inside the folder dir.
//
// dir is compared as a whole path prefix ("<dir>/"), never as a LIKE pattern: "Teen
// Titans" doesn't match "Teen Titans Go!", a season folder's name ("S01") matches only
// that one folder rather than every show with an S01 inside it, and a "_" or "%" in a
// folder name is just a character. A read error answers as shared ([]int64{0}): the caller
// then refuses or keeps the year, which is the safe answer.
//
// fix/series-folder-routing adds Repo.OtherSeriesUnder with the same rule; when that
// branch is merged this becomes a call to it.
func (s *Service) OtherShowsIn(ctx context.Context, seriesID int64, dir string, limit int) []int64 {
	if dir == "" {
		return nil
	}
	if limit <= 0 {
		limit = 1
	}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	rows, err := s.repo.db.QueryContext(ctx,
		`SELECT DISTINCT series_id FROM episodes
		 WHERE series_id <> ?1 AND has_file = 1 AND substr(file_path, 1, length(?2)) = ?2 LIMIT ?3`,
		seriesID, prefix, limit)
	if err != nil {
		return []int64{0}
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	if rows.Err() != nil && len(out) == 0 {
		return []int64{0}
	}
	return out
}
