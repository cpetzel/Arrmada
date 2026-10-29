package library

import (
	"path/filepath"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// ParseEpisodeFile reads a video file already on disk, by its full path, for the episode it
// holds: parser.ParseLibraryFile with the season its parent folder names. It is for files
// judged where they sit — a rescan, the library scan, the folder scan of a manual import —
// never for a release name, which has no folder to confirm its season.
func ParseEpisodeFile(path string) parser.Release {
	season, inSeason := SeasonFolderNumber(filepath.Base(filepath.Dir(path)))
	return parser.ParseLibraryFile(filepath.Base(path), season, inSeason)
}

// ReadOnlyOnDisk reports whether the file at path holds an episode only by
// ParseEpisodeFile's on-disk reading (#81's single-digit "1x8"), not by its name alone as a
// release is read. Such a file only fills an episode with no file: it never takes the place
// of the file an episode already has.
func ReadOnlyOnDisk(path string) bool {
	if len(parser.Parse(filepath.Base(path)).Episodes) > 0 {
		return false
	}
	return len(ParseEpisodeFile(path).Episodes) > 0
}
