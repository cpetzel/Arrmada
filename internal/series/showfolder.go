package series

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Which folder a show lives in (#56).
//
// v0.3.1 read a show's folder off its first recorded episode file: two folders up when
// the file's folder was named "Season N" or "Specials", one up otherwise, and imported
// into <TV folder>/<that folder's name>. Libraries whose season folders are called "S01",
// "Stagione 1", "Saison 1" or anything else got the season folder back as the show's
// folder, so the next import went to <TV folder>/S13/Season 12/… — a folder every show
// with an "S13" then shared.
//
// This keeps v0.3.1's answer exactly, and never widens it. When that answer is the
// mistake — the folder is named like a season, or is the TV folder itself — the show is
// "#56-shaped": nothing guesses its folder. A new episode of a season the show already
// has goes into the one folder that season's recorded files are in (SeasonDir), and
// everything else that needs the show's folder (a new season, rescan, rename, duplicates,
// Move to folder) is refused with a message saying how to fix the folders.

// SetSeasonFolderMatcher adds the configured season folder template to what counts as a
// season folder (library.Importer.IsSeasonFolderName), so a library Arrmada itself names
// "{title} Season {season}" is read right. Call it at startup, before anything runs.
func (s *Service) SetSeasonFolderMatcher(fn func(name string) bool) { s.seasonFolder = fn }

// seasonLike reports whether a folder name is a season folder (Specials included) —
// something inside a show's folder, which can never be the show's own folder. Extras
// folders don't count here: "Extras" is a show too.
func (s *Service) seasonLike(name string) bool {
	if _, ok := library.SeasonFolderNumber(name); ok {
		return true
	}
	return s.seasonFolder != nil && s.seasonFolder(name)
}

// ShowFolder is the show folder an episode file lives in, judged by folder names alone:
// one level up from a season folder ("Season 04", "S04", "Stagione 4", "Specials"), the
// file's own folder otherwise.
func ShowFolder(episodePath string) string {
	parent := filepath.Dir(episodePath)
	if library.LooksLikeSeasonFolder(filepath.Base(parent)) {
		return filepath.Dir(parent)
	}
	return parent
}

// reV031Season is the season folder pattern v0.3.1 knew.
var reV031Season = regexp.MustCompile(`(?i)^(season[ ._-]?\d+|specials)$`)

// v031Folder is the folder v0.3.1 read as a show's from one of its episode files.
func v031Folder(path string) string {
	parent := filepath.Dir(filepath.Clean(path))
	if reV031Season.MatchString(filepath.Base(parent)) {
		return filepath.Dir(parent)
	}
	return parent
}

// TVRoots is every TV folder now, the default first.
func (s *Service) TVRoots() []string { return s.libRoots() }

// rootOf is the TV folder holding path (the deepest, when one is inside another) and
// whether there is one.
func rootOf(roots []string, path string) (string, bool) {
	best := ""
	for _, r := range roots {
		if strings.TrimSpace(r) == "" || !libfolders.StrictlyUnder(path, r) {
			continue
		}
		if len(filepath.Clean(r)) > len(best) {
			best = filepath.Clean(r)
		}
	}
	return best, best != ""
}

// shape56 reports whether v0.3.1's folder f, read off the file at path, is the #56
// mistake: named like a season, or the TV folder itself (or above it).
func (s *Service) shape56(roots []string, path, f string) bool {
	if s.seasonLike(filepath.Base(f)) {
		return true
	}
	root, inRoot := rootOf(roots, path)
	return inRoot && !libfolders.StrictlyUnder(f, root)
}

// ShowFolderFor is the show folder the episode file at path is in, for Plex scans and
// deletes: the folder v0.3.1 read, or — when that's the #56 mistake — the file's own
// folder, never one above it. "" for a file loose in a TV folder.
func (s *Service) ShowFolderFor(path string) string {
	roots := s.libRoots()
	f := v031Folder(path)
	if !s.shape56(roots, path, f) {
		return f
	}
	dir := filepath.Dir(filepath.Clean(path))
	if root, inRoot := rootOf(roots, path); inRoot && !libfolders.StrictlyUnder(dir, root) {
		return "" // loose in a TV folder
	}
	return dir
}

// ShowFoldersOf answers, for each episode file of show seriesID, the show folder it is in
// (ShowFolderFor). Move to folder refuses a #56-shaped show before it gets here
// (FolderUnclear).
func (s *Service) ShowFoldersOf(context.Context, int64) func(path string) string {
	return s.ShowFolderFor
}

// ShowFolderInfo is where a show lives on disk, as far as its first recorded file tells.
type ShowFolderInfo struct {
	// Path is the show's folder, as v0.3.1 read it ("" when it has no files, or Unknown).
	Path string `json:"path"`
	// Unknown: the show's folder can't be told — it's #56-shaped (Shape56), or its files
	// couldn't be read. Nothing that needs the show's folder may run; Why says why.
	Unknown bool   `json:"unknown"`
	Why     string `json:"why,omitempty"`
	Shape56 bool   `json:"shape56,omitempty"`
	// FileDir is the first recorded file's own folder, for a #56-shaped show: what Plex is
	// pointed at instead of a folder above it.
	FileDir string `json:"file_dir,omitempty"`
	// ownDir: FileDir is also the folder v0.3.1 read for the show — the file sits right in
	// it (not in a "Season N" folder below it).
	ownDir bool
}

// msg56 is what a refused #56-shaped show says.
func msg56(f string) string {
	return fmt.Sprintf("its season folders are named like seasons (%s), so Arrmada won't guess its folder; "+
		"new episodes of a season it already has still import, and the rest wait in Needs you. "+
		"Rename the season folders to \"Season 01\", \"Season 02\"… and rescan the show", filepath.Base(f))
}

// ShowFolderOf is the folder a show lives in: v0.3.1's answer from its first recorded
// file, or Unknown when that answer is the #56 mistake.
func (s *Service) ShowFolderOf(ctx context.Context, seriesID int64) ShowFolderInfo {
	first, err := s.repo.AnyEpisodeFilePath(ctx, seriesID)
	if err != nil {
		return ShowFolderInfo{Unknown: true, Why: "the show's files couldn't be read: " + err.Error()}
	}
	if first == "" {
		return ShowFolderInfo{}
	}
	roots := s.libRoots()
	f := v031Folder(first)
	if !s.shape56(roots, first, f) {
		return ShowFolderInfo{Path: f}
	}
	// The first recorded file gives the #56 mistake. When it's gone — moved back by hand,
	// its season folders renamed to "Season N" — v0.3.1 would keep reading the stale
	// record; the first recorded file still on disk speaks instead, so renaming the
	// folders and rescanning (FollowRenamedSeasonFolders) is a way out.
	if _, err := os.Lstat(first); errors.Is(err, fs.ErrNotExist) {
		if paths, err := s.repo.EpisodeFilePaths(ctx, seriesID); err == nil {
			for _, p := range paths {
				if _, err := os.Lstat(p); err != nil {
					continue
				}
				if pf := v031Folder(p); !s.shape56(roots, p, pf) {
					return ShowFolderInfo{Path: pf}
				}
				break // the first file still there is the #56 mistake too
			}
		}
	}
	info := ShowFolderInfo{Unknown: true, Shape56: true, Why: msg56(f)}
	dir := filepath.Dir(filepath.Clean(first))
	if root, ok := rootOf(roots, first); ok && libfolders.StrictlyUnder(dir, root) {
		info.FileDir = dir
		info.ownDir = filepath.Clean(f) == dir
	}
	return info
}

// FolderUnclear says why the show's folder can't be used ("" when it can): for Move to
// folder and anything else that would act on the whole folder.
func (s *Service) FolderUnclear(ctx context.Context, seriesID int64) string {
	if info := s.ShowFolderOf(ctx, seriesID); info.Unknown {
		return info.Why
	}
	return ""
}

// SeasonDir is, for a #56-shaped show, the folder a new episode of season goes into: the
// one folder all of that season's recorded files are in. It refuses (with why) when there
// is none — a new season, the season's files in several folders, or only gone files — and
// when the folder isn't the show's alone: right in or above a TV folder, inside a folder
// named like a season (<TV>/S13/Season 12, a #56 leftover), or holding anything other than
// the show's recorded files (another video, a folder with videos, a link). It never names
// a folder above the files, and never one that doesn't exist.
func (s *Service) SeasonDir(ctx context.Context, seriesID int64, season int) (string, string) {
	paths, err := s.repo.SeasonFilePaths(ctx, seriesID, season)
	if err != nil {
		return "", "the show's files couldn't be read: " + err.Error()
	}
	if len(paths) == 0 {
		return "", fmt.Sprintf("it has no files of season %d yet, so there's no season folder to put the episode in", season)
	}
	dir := filepath.Dir(filepath.Clean(paths[0]))
	for _, p := range paths[1:] {
		if filepath.Dir(filepath.Clean(p)) != dir {
			return "", fmt.Sprintf("its season %d files are in more than one folder", season)
		}
	}
	root, inRoot := rootOf(s.libRoots(), dir+string(filepath.Separator)+"x")
	if !inRoot || !libfolders.StrictlyUnder(dir, root) {
		return "", fmt.Sprintf("its season %d files aren't in a folder of their own inside a TV folder", season)
	}
	if s.seasonLike(filepath.Base(filepath.Dir(dir))) {
		return "", fmt.Sprintf("its season %d files are in %s, inside a folder named like a season", season, dir)
	}
	all, err := s.repo.EpisodeFilePaths(ctx, seriesID)
	if err != nil {
		return "", "the show's files couldn't be read: " + err.Error()
	}
	tracked := make(map[string]bool, len(all))
	for _, p := range all {
		tracked[filepath.Clean(p)] = true
	}
	if why := s.onlyShowFiles(dir, tracked); why != "" {
		return "", why
	}
	return dir, ""
}

// onlyShowFiles says why dir holds more than the show's recorded files ("" when it
// doesn't): a video that isn't one of them, a folder with videos in it, or a link.
func (s *Service) onlyShowFiles(dir string, tracked map[string]bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Sprintf("%s can't be read", dir)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.Type()&fs.ModeSymlink != 0:
			return fmt.Sprintf("%s holds a link, %s", dir, e.Name())
		case e.IsDir():
			if videoOrLinkIn(p) {
				return fmt.Sprintf("%s holds a folder with videos in it, %s", dir, e.Name())
			}
		case library.IsVideoFile(e.Name()) && !tracked[p]:
			return fmt.Sprintf("%s holds a video that isn't one of the show's, %s", dir, e.Name())
		}
	}
	return ""
}

// videoOrLinkIn reports whether dir holds, at any depth and hidden folders included, a
// video file or a link (which could lead to anyone's videos). An unreadable folder counts.
func videoOrLinkIn(dir string) bool {
	found := false
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			found = true
			return filepath.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && library.IsVideoFile(d.Name())) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found || err != nil
}

// FollowRenamedSeasonFolders is the way out for a #56-shaped show: once its season folders
// are renamed to "Season 01", "Season 02"… (or "Specials"), each recorded file that's gone
// from its old season-named folder is re-pointed at the same file name in the renamed
// folder beside it — only that exact file, only in a folder v0.3.1 itself reads as that
// season's, only when there's exactly one. It returns how many episodes it re-pointed.
// Nothing else changes; a rescan then works on the show as it always has.
func (s *Service) FollowRenamedSeasonFolders(ctx context.Context, seriesID int64) int {
	seasons, err := s.repo.SeasonsFor(ctx, seriesID)
	if err != nil {
		return 0
	}
	roots := s.libRoots()
	moved := 0
	for _, sn := range seasons {
		for _, e := range sn.Episodes {
			if !e.HasFile || e.FilePath == "" {
				continue
			}
			p := filepath.Clean(e.FilePath)
			if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
				continue // still there (or can't tell): nothing to follow
			}
			old := filepath.Dir(p)
			if !s.seasonLike(filepath.Base(old)) {
				continue
			}
			show := filepath.Dir(old)
			if root, ok := rootOf(roots, p); !ok || !libfolders.StrictlyUnder(show, root) {
				continue
			}
			entries, err := os.ReadDir(show)
			if err != nil {
				continue
			}
			var found []string
			for _, d := range entries {
				if !d.IsDir() || !reV031Season.MatchString(d.Name()) {
					continue
				}
				if n, ok := library.SeasonFolderNumber(d.Name()); !ok || n != e.SeasonNumber {
					continue
				}
				cand := filepath.Join(show, d.Name(), filepath.Base(p))
				if fi, err := os.Lstat(cand); err == nil && fi.Mode().IsRegular() {
					found = append(found, cand)
				}
			}
			if len(found) != 1 {
				continue
			}
			fi, _ := os.Stat(found[0])
			if err := s.repo.SetEpisodeFile(ctx, seriesID, e.SeasonNumber, e.EpisodeNumber, found[0], fi.Size()); err != nil {
				continue
			}
			moved++
		}
	}
	if moved > 0 {
		s.repo.AddEvent(ctx, seriesID, "files.followed", fmt.Sprintf("Followed %d episode file%s into renamed season folders", moved, map[bool]string{true: "", false: "s"}[moved == 1]))
	}
	return moved
}

// Why a folder can't take a show's episodes.
const (
	unsoundSeason = "season" // named like a season folder
	unsoundRoot   = "root"   // a TV folder, or named after one
	unsoundShared = "shared" // other shows' files are in it
)

// folderVerdict is a folder judged: why it can't take the show's episodes ("" when it
// can) and the kind of reason (unsound*).
type folderVerdict struct {
	why, kind string
}

// judgeFolder says whether dir can take show seriesID's episodes.
func (s *Service) judgeFolder(ctx context.Context, seriesID int64, dir string) folderVerdict {
	roots := s.libRoots()
	base := filepath.Base(dir)
	if s.seasonLike(base) {
		return folderVerdict{why: fmt.Sprintf("%s is named like a season folder", dir), kind: unsoundSeason}
	}
	for _, r := range roots {
		if filepath.Clean(r) == filepath.Clean(dir) {
			return folderVerdict{why: fmt.Sprintf("%s is a TV folder itself", dir), kind: unsoundRoot}
		}
	}
	// A folder named after the TV folder it's in (<TV>/TV/…) is what the old flat-layout
	// bug made. Only that TV folder's name counts.
	if root, ok := rootOf(roots, dir); ok && strings.EqualFold(filepath.Base(root), base) {
		return folderVerdict{why: fmt.Sprintf("%s is named after the TV folder %s", dir, root), kind: unsoundRoot}
	}
	if others := s.repo.OtherSeriesUnder(ctx, seriesID, dir, 3); len(others) > 0 {
		return folderVerdict{why: fmt.Sprintf("%s also holds episodes of %s", dir, s.describeSeries(ctx, others)), kind: unsoundShared}
	}
	return folderVerdict{}
}

// describeSeries names a few shows for a message.
func (s *Service) describeSeries(ctx context.Context, ids []int64) string {
	var names []string
	for _, id := range ids {
		if sr, err := s.repo.Get(ctx, id); err == nil {
			names = append(names, fmt.Sprintf("%q", sr.Title))
		}
	}
	if len(names) == 0 {
		return "other shows"
	}
	if len(ids) >= 3 {
		return strings.Join(names, ", ") + " and possibly more"
	}
	return strings.Join(names, " and ")
}

// ShowFolderError is a show whose episodes can't be imported because there is no sound
// folder to put them in. The download goes to Review with Error() as its reason.
type ShowFolderError struct {
	Title string
	Why   string
}

// ErrNoShowFolder is what every ShowFolderError is (errors.Is), for handlers to answer
// with a conflict rather than a server fault.
var ErrNoShowFolder = errors.New("no safe folder for the show")

// Unwrap makes a ShowFolderError an ErrNoShowFolder.
func (e *ShowFolderError) Unwrap() error { return ErrNoShowFolder }

func (e *ShowFolderError) Error() string {
	return fmt.Sprintf("Can't tell which folder %s lives in: %s.", e.Title, e.Why)
}

// ImportDir is the TV folder the show's episodes are placed under (with
// ExistingFolderName as the show folder), as v0.3.1 chose it: the one its folder is in,
// else the one it's assigned to. "" is the importer's default TV folder.
func (s *Service) ImportDir(ctx context.Context, sr Series) string {
	return s.ImportDirOf(ctx, sr, s.ShowFolderOf(ctx, sr.ID))
}

// ImportDirOf is ImportDir for a show whose folder was already worked out (info).
func (s *Service) ImportDirOf(ctx context.Context, sr Series, info ShowFolderInfo) string {
	if s.folders == nil {
		return ""
	}
	if p := info.Path; p != "" {
		if f, ok := s.folders.Owner(ctx, libfolders.MediaSeries, p); ok {
			return f.Path
		}
	}
	return s.folders.Path(ctx, libfolders.MediaSeries, sr.LibraryFolderID)
}

// UnsafeImportFolder says why dir must not take sr's episodes ("" when it may): it's
// named like a season folder, it's a TV folder (or named after one), or another show's
// files are in it. The last check before any episode file is placed.
func (s *Service) UnsafeImportFolder(ctx context.Context, sr Series, dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return s.judgeFolder(ctx, sr.ID, filepath.Clean(dir)).why
}
