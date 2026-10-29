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
