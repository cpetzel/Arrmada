package automation

import (
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// placementFolder is the show folder one placement writes every file into: existing, the
// folder the show already has, or — for a show with none — a new folder named once, here,
// by library.Importer.NewSeriesFolderName.
//
// Naming the folder once per placement and passing it down is what keeps a download in one
// folder: with "Add the year only when it's needed" on, the year-or-not answer can change
// between files (TMDB down and back, a namesake added mid-pack), and a per-file answer
// split a pack across "Show" and "Show (2024)". Anything that checks where a placement's
// files go must check this same name.
//
// When fix/series-folder-routing is merged, seriesPlacement applies this to the folder it
// returns, before its safety check, so the check and the write use one name.
func placementFolder(imp *library.Importer, existing string, s series.Series) string {
	if existing != "" || imp == nil {
		return existing
	}
	return imp.NewSeriesFolderName(s.Title, s.Year)
}
