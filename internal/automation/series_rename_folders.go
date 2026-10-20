package automation

import (
	"context"
	"path/filepath"
	"strings"
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
// "Crossing" is read from the paths themselves, not from folder names: a move crosses when
// the file's first folder under the TV folder (root) isn't the target's. Recognising season
// folders by name ("Season 1") misread "S01", "Staffel 1" and "Saison 1" as show folders and
// refused ordinary renames inside one show. Whether another series is in the target's show
// folder is a path-prefix match on the recorded files (OtherShowsIn), never a LIKE pattern.
//
// It runs on the rows before planning, like holdUnpreviewed, so a held file counts as
// staying put and nothing is planned onto it. A file's move is decided by its first row.
func (c *Coordinator) holdOtherShowsFolders(ctx context.Context, seriesID int64, root string, rows []renameRow) ([]renameRow, []RenameSkip) {
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
			to, toOK := firstFolderUnder(root, r.Target)
			from, fromOK := firstFolderUnder(root, r.Path)
			if toOK && fromOK && from == to {
				continue // a rename inside the show's own folder
			}
			isForeign := true // a target outside the TV folder can't be vouched for
			if toOK {
				dir := filepath.Join(root, to)
				seen := false
				if isForeign, seen = foreign[dir]; !seen {
					isForeign = len(c.series.OtherShowsIn(ctx, seriesID, dir, 1)) > 0
					foreign[dir] = isForeign
				}
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

// firstFolderUnder is the name of the folder directly under root that path is inside —
// the show folder, whatever the season folders below it are called — and false when path
// isn't inside a folder under root.
func firstFolderUnder(root, path string) (string, bool) {
	if strings.TrimSpace(root) == "" {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if len(parts) < 2 || parts[0] == "" {
		return "", false // a file sitting loose in the TV folder has no show folder
	}
	return parts[0], true
}
