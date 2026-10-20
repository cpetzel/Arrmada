package series

import "github.com/tristenlammi/arrmada/internal/metadata"

// ambiguousYearless reports whether a scanned folder named title, with no year, matches
// more than one show's title exactly — so only the user can say which show it is. A folder
// with a year is told apart by it (TitleYearMatch), and a single exact match is safe.
//
// "Add the year only when it's needed" makes year-less folders common, which is why this
// matters: guessing used to file a year-less "Dark Matter" folder of 2015 episodes as the
// 2024 show.
func ambiguousYearless(results []metadata.SeriesResult, title string, year int) bool {
	if year > 0 {
		return false
	}
	want := metadata.NormalizeTitle(title)
	if want == "" {
		return false
	}
	ids := map[int]bool{}
	for _, r := range results {
		if metadata.NormalizeTitle(r.Title) == want {
			ids[r.TMDBID] = true
		}
	}
	return len(ids) > 1
}
