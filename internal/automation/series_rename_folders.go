package automation

import (
	"context"
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/series"
)

// reasonOtherShowsFolder is a rename refused because it would carry a file into another
// show's folder.
const reasonOtherShowsFolder = "another show's files are in that folder — two shows in one folder mix their seasons"

// holdOtherShowsFolders keeps a file where it is when its rename would carry it into a
// different show folder that another series keeps files in.
//
// A rename names every file inside the folder the show already has (its existing folder is
// passed in, never re-derived from the template), so the only rename that crosses show
// folders is one that gathers a show split over two folders into the one it uses. If that
// folder also holds another show's files, gathering more into it merges the two shows
// further — their season folders mix and any episode number they share collides. Which
// show owns the folder is the user's call, so the move is refused, not "fixed".
//
// It runs on the rows before planning, like holdUnpreviewed, so a held file counts as
// staying put and nothing is planned onto it. A file's move is decided by its first row.
func (c *Coordinator) holdOtherShowsFolders(ctx context.Context, seriesID int64, rows []renameRow) ([]renameRow, []RenameSkip) {
	skipped := []RenameSkip{}
	if c.series == nil {
		return rows, skipped
	}
	foreign := map[string]bool{} // target show folder → another series keeps files there
	held := map[string]bool{}    // file path → held
	decided := map[string]bool{}
	out := make([]renameRow, len(rows))
	for i, r := range rows {
		out[i] = r
		if !decided[r.Path] {
			decided[r.Path] = true
			if r.Target == "" || r.Target == r.Path {
				continue
			}
			to := series.ShowFolder(r.Target)
			if filepath.Clean(to) == filepath.Clean(series.ShowFolder(r.Path)) {
				continue // a rename inside the show's own folder
			}
			isForeign, seen := foreign[to]
			if !seen {
				isForeign = len(c.series.FolderSharedWith(ctx, seriesID, filepath.Base(to))) > 0
				foreign[to] = isForeign
			}
			if isForeign {
				held[r.Path] = true
				skipped = append(skipped, RenameSkip{From: r.Path, To: r.Target, Season: r.Season, Episode: r.Episode, Reason: reasonOtherShowsFolder})
			}
		}
		if held[r.Path] {
			out[i].Target = r.Path
		}
	}
	return out, skipped
}
