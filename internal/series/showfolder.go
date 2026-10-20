package series

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/library"
)

// Which folder a show lives in (#56).
//
// v0.3.1 read a show's folder off ONE episode file: two folders up when the file's folder
// was named "Season N" or "Specials", one up otherwise, and imported into
// <TV folder>/<that folder's name>. Libraries whose season folders are called "S01",
// "Stagione 1", "Saison 1" or anything else got the season folder back as the show's
// folder, so the next import went to <TV folder>/S13/Season 12/… — a folder every show
// with an "S13" then shared — and once one file was there, every later import followed it.
//
// This keeps v0.3.1's answer wherever it was sound, and changes it only for that mistake:
//   - each file gives v0.3.1's folder;
//   - when that folder is named like a season (or is the TV folder itself), the file's one
//     recognised season folder is stepped out of instead, and the folder above it is used
//     only when it sits right inside the TV folder and isn't named like a season;
//   - a file that still has no sound folder is a leftover of the bug and is ignored, as
//     long as the show's other files agree on one folder;
//   - files that disagree, a folder other shows' files are in, or a show whose every file
//     is a leftover: nothing is guessed, and the download goes to Review.

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

// insideShow reports whether a folder name is one a show keeps inside its folder: a
// season folder, Specials, or Extras.
func (s *Service) insideShow(name string) bool {
	return library.LooksLikeSeasonFolder(name) || s.seasonLike(name)
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

// fileVerdict is what one episode file says about its show's folder.
type fileVerdict struct {
	dir string // the show's folder (ok)
	ok  bool
	// leftover: a #56 leftover — the file's folder is named like a season (or is the TV
	// folder) and there's no folder one season folder up that could be the show's.
	leftover bool
	why      string
}

// fileFolder is the show folder one episode file gives (see the top of this file). The
// folder above a season-named folder is used only when vouch says so: it must hold this
// show and nothing else (onlyThisShow), or a category like <TV>/Kids/S01 next to an
// untracked <TV>/Kids/Bluey would become the show's folder. nil vouch never corrects.
func (s *Service) fileFolder(roots []string, path string, vouch func(up string) bool) fileVerdict {
	path = filepath.Clean(path)
	f := v031Folder(path)
	root, inRoot := rootOf(roots, path)
	atRoot := inRoot && !libfolders.StrictlyUnder(f, root)
	if !s.seasonLike(filepath.Base(f)) && !atRoot {
		return fileVerdict{dir: f, ok: true} // v0.3.1's own answer
	}
	parent := filepath.Dir(path)
	if s.seasonLike(filepath.Base(parent)) {
		up := filepath.Dir(parent)
		if inRoot && filepath.Clean(filepath.Dir(up)) == root && !s.seasonLike(filepath.Base(up)) {
			if vouch != nil && vouch(up) {
				return fileVerdict{dir: up, ok: true}
			}
			return fileVerdict{why: fmt.Sprintf("%s holds more than this show's season folders", up)}
		}
	}
	if atRoot {
		return fileVerdict{leftover: true, why: fmt.Sprintf("%s sits loose in the TV folder %s", path, root)}
	}
	return fileVerdict{leftover: true, why: fmt.Sprintf("%s is named like a season folder", f)}
}

// onlyThisShow reports whether dir, on disk, holds this show and nothing else: every
// folder in it holding videos (at any depth) is a season folder holding some of the show's
// recorded files (tracked) or an extras folder, and every video right in it is one of the
// show's. Another show's folder — tracked or not — or a folder Arrmada can't place means
// no. A folder that can't be read means no.
func (s *Service) onlyThisShow(dir string, tracked map[string]bool) bool {
	entries, err := s.readDirFn()(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || library.SkipScanDir(name) {
			continue
		}
		p := filepath.Join(dir, name)
		if !e.IsDir() {
			if library.IsVideoFile(name) && !tracked[p] {
				return false
			}
			continue
		}
		if !s.holdsVideo(p, 8) {
			continue
		}
		if s.seasonLike(name) {
			if holdsTracked(p, tracked) {
				continue
			}
			return false
		}
		if library.LooksLikeSeasonFolder(name) {
			continue // Extras, Featurettes…
		}
		return false
	}
	return true
}

// holdsTracked reports whether any of the paths in tracked is inside dir.
func holdsTracked(dir string, tracked map[string]bool) bool {
	for p := range tracked {
		if libfolders.StrictlyUnder(p, dir) {
			return true
		}
	}
	return false
}

// readDirFn is how folders are listed: os.ReadDir, or a test's stand-in.
func (s *Service) readDirFn() func(string) ([]fs.DirEntry, error) {
	if s.readDir != nil {
		return s.readDir
	}
	return os.ReadDir
}

// holdsVideo reports whether dir holds a video file within depth folders.
func (s *Service) holdsVideo(dir string, depth int) bool {
	entries, err := s.readDirFn()(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			if library.IsVideoFile(e.Name()) {
				return true
			}
			continue
		}
		if depth > 1 && !library.SkipScanDir(e.Name()) && s.holdsVideo(filepath.Join(dir, e.Name()), depth-1) {
			return true
		}
	}
	return false
}

// trackedSet is a show's recorded files, cleaned.
func trackedSet(paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[filepath.Clean(p)] = true
	}
	return out
}

// ShowFolderFor is the folder v0.3.1 read as the show's from the episode file at path
// ("" for a file loose in a TV folder). For Plex scans and deletes, which have no show to
// check a corrected folder against.
func (s *Service) ShowFolderFor(path string) string {
	f := v031Folder(path)
	if root, inRoot := rootOf(s.libRoots(), path); inRoot && !libfolders.StrictlyUnder(f, root) {
		return "" // loose in a TV folder
	}
	return f
}

// ShowFoldersOf answers, for each episode file of show seriesID, the show folder it is in:
// its folder as ShowFolderOf reads it (the folder above a season-named folder only when
// that holds this show alone), else v0.3.1's (ShowFolderFor). For Move to folder, which
// moves a show's folders whole.
func (s *Service) ShowFoldersOf(ctx context.Context, seriesID int64) func(path string) string {
	paths, _ := s.repo.EpisodeFilePaths(ctx, seriesID)
	tracked := trackedSet(paths)
	roots := s.libRoots()
	vouch := func(up string) bool { return s.onlyThisShow(up, tracked) }
	return func(path string) string {
		if v := s.fileFolder(roots, path, vouch); v.ok {
			return v.dir
		}
		return s.ShowFolderFor(path)
	}
}

// ShowFolderInfo is where a show lives on disk, as far as its episode files tell.
type ShowFolderInfo struct {
	// Path is the show's folder ("" when it has no files).
	Path string `json:"path"`
	// Files is how many of the show's files are in Path; Total how many it has.
	Files int `json:"files"`
	Total int `json:"total"`
	// Unknown: the show has files, but they don't name one sound folder — their folders
	// are named like a season, sit loose in the TV folder, disagree with each other, or
	// hold other shows' files too. Nothing may be imported for the show until that is
	// sorted out; Why says what was found, in plain words.
	Unknown bool   `json:"unknown"`
	Why     string `json:"why,omitempty"`
}

// Why a folder can't take a show's episodes.
const (
	unsoundSeason = "season" // named like a season folder
	unsoundRoot   = "root"   // a TV folder, or named after one
	unsoundShared = "shared" // other shows' files are in it
)

// folderJudge decides which folders can be a show's own. others lists up to a few OTHER
// series with files under a folder; names describes them for a message.
type folderJudge struct {
	svc    *Service
	roots  []string
	others func(dir string) []int64
	names  func(ids []int64) string
	// disk: folders may be looked at on disk (onlyThisShow); without it the folder above a
	// season-named folder is never vouched for.
	disk bool
}

// folderVerdict is a folder judged: why it can't be a show's own ("" when it can) and the
// kind of reason (unsound*).
type folderVerdict struct {
	why, kind string
}

func (j *folderJudge) judge(dir string) folderVerdict {
	base := filepath.Base(dir)
	if j.svc.seasonLike(base) {
		return folderVerdict{why: fmt.Sprintf("%s is named like a season folder", dir), kind: unsoundSeason}
	}
	for _, r := range j.roots {
		if filepath.Clean(r) == filepath.Clean(dir) {
			return folderVerdict{why: fmt.Sprintf("%s is a TV folder itself", dir), kind: unsoundRoot}
		}
	}
	// A folder named after the TV folder it's in (<TV>/TV/…) is what the old flat-layout
	// bug made. Only that TV folder's name counts.
	if root, ok := rootOf(j.roots, dir); ok && strings.EqualFold(filepath.Base(root), base) {
		return folderVerdict{why: fmt.Sprintf("%s is named after the TV folder %s", dir, root), kind: unsoundRoot}
	}
	if others := j.others(dir); len(others) > 0 {
		names := "other shows"
		if j.names != nil {
			names = j.names(others)
		}
		return folderVerdict{why: fmt.Sprintf("%s also holds episodes of %s", dir, names), kind: unsoundShared}
	}
	return folderVerdict{}
}

// dbJudge judges folders for one show against the database.
func (s *Service) dbJudge(ctx context.Context, seriesID int64) *folderJudge {
	return &folderJudge{
		svc:    s,
		roots:  s.libRoots(),
		others: func(dir string) []int64 { return s.repo.OtherSeriesUnder(ctx, seriesID, dir, 3) },
		names:  func(ids []int64) string { return s.describeSeries(ctx, ids) },
		disk:   true,
	}
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

// importKey is where v0.3.1 put a show whose files are in dir: the folder of that name in
// the TV folder holding dir ("" root: the show's assigned one).
func importKey(roots []string, dir string) string {
	root, _ := rootOf(roots, dir)
	return root + "\x00" + filepath.Base(dir)
}

// showFolderFrom is ShowFolderOf for a show's files, judged by j.
func (s *Service) showFolderFrom(paths []string, j *folderJudge) ShowFolderInfo {
	info := ShowFolderInfo{Total: len(paths)}
	if len(paths) == 0 {
		return info
	}
	byKey := map[string]map[string]int{} // import key → folder → files
	var whys, unsure []string
	tracked := trackedSet(paths)
	vouch := func(up string) bool { return j.disk && s.onlyThisShow(up, tracked) }
	for _, p := range paths {
		v := s.fileFolder(j.roots, p, vouch)
		if !v.ok {
			whys = append(whys, v.why)
			if !v.leftover {
				unsure = append(unsure, v.why)
			}
			continue
		}
		k := importKey(j.roots, v.dir)
		if byKey[k] == nil {
			byKey[k] = map[string]int{}
		}
		byKey[k][v.dir]++
	}
	// A season-named folder whose folder above can't be vouched for: nothing is guessed,
	// whatever the other files say.
	if len(unsure) > 0 {
		info.Unknown, info.Why = true, firstDistinct(unsure)
		return info
	}
	switch len(byKey) {
	case 0:
		info.Unknown, info.Why = true, firstDistinct(whys)
		return info
	case 1:
	default:
		var dirs []string
		for _, ds := range byKey {
			for d := range ds {
				dirs = append(dirs, d)
			}
		}
		sort.Strings(dirs)
		info.Unknown = true
		info.Why = fmt.Sprintf("its files are in different folders (%s and %s)", dirs[0], dirs[len(dirs)-1])
		return info
	}
	var dirs map[string]int
	for _, ds := range byKey {
		dirs = ds
	}
	best := ""
	for d, n := range dirs {
		if best == "" || n > dirs[best] || (n == dirs[best] && d < best) {
			best = d
		}
		info.Files += n
	}
	if v := j.judge(best); v.why != "" {
		info.Unknown, info.Why, info.Files = true, v.why, 0
		return info
	}
	info.Path = best
	return info
}

// ShowFolderOf works out the folder a show lives in from all of its episode files (see the
// top of this file).
func (s *Service) ShowFolderOf(ctx context.Context, seriesID int64) ShowFolderInfo {
	paths, err := s.repo.EpisodeFilePaths(ctx, seriesID)
	if err != nil {
		return ShowFolderInfo{Unknown: true, Why: "the show's files couldn't be read: " + err.Error()}
	}
	kept := s.dropGoneMisplaced(paths, s.libRoots())
	if len(paths) > 0 && len(kept) == 0 {
		// Every file it had was a #56 leftover that's gone now — moved back by hand, deleted,
		// or on a share that's down. Where the show lives isn't known; it isn't a new show.
		return ShowFolderInfo{Total: len(paths), Unknown: true, Why: "the files it had are no longer where they were recorded; rescan it once they're back in its folder"}
	}
	return s.showFolderFrom(kept, s.dbJudge(ctx, seriesID))
}

// dropGoneMisplaced leaves out leftovers of #56 that are no longer there — moved back by
// hand before a rescan caught up — so they don't keep a show from its folder. Only those
// files are looked at on disk; every other path is kept as recorded, so a share that's
// down never changes where a show lives.
func (s *Service) dropGoneMisplaced(paths []string, roots []string) []string {
	gone := s.fileGone
	if gone == nil {
		gone = func(p string) bool {
			_, err := os.Stat(p)
			return errors.Is(err, fs.ErrNotExist)
		}
	}
	out := paths[:0:0]
	for _, p := range paths {
		if v := s.fileFolder(roots, p, nil); v.leftover && gone(p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// MisplacedFile is one of a show's episode files that #56 left in a folder no show should
// own: one named like a season right in the TV folder (<TV>/S13/Season 12), or loose in it.
type MisplacedFile struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// misplacedFrom lists the misplaced files among a show's files (see MisplacedFile).
func (s *Service) misplacedFrom(paths, roots []string) []MisplacedFile {
	var out []MisplacedFile
	for _, p := range paths {
		if v := s.fileFolder(roots, p, nil); v.leftover {
			out = append(out, MisplacedFile{Path: p, Why: v.why})
		}
	}
	return out
}

// MisplacedFiles is where the show lives and its episode files that #56 misplaced.
func (s *Service) MisplacedFiles(ctx context.Context, seriesID int64) (ShowFolderInfo, []MisplacedFile, error) {
	paths, err := s.repo.EpisodeFilePaths(ctx, seriesID)
	if err != nil {
		return ShowFolderInfo{}, nil, err
	}
	roots := s.libRoots()
	paths = s.dropGoneMisplaced(paths, roots)
	info := s.showFolderFrom(paths, s.dbJudge(ctx, seriesID))
	return info, s.misplacedFrom(paths, roots), nil
}

// MisplacedShow is a show with episode files #56 misplaced.
type MisplacedShow struct {
	SeriesID int64  `json:"series_id"`
	Title    string `json:"title"`
	Files    int    `json:"files"`
	// NoFolder: none of its files give it a folder of its own.
	NoFolder bool `json:"no_folder"`
}

// MisplacedShows lists every show with misplaced episode files, worked out from the
// database alone in one pass — it never looks at the disk, so it can't wake a sleeping
// array.
func (s *Service) MisplacedShows(ctx context.Context) ([]MisplacedShow, error) {
	rows, err := s.repo.AllEpisodeFilePaths(ctx)
	if err != nil {
		return nil, err
	}
	roots := s.libRoots()
	byShow := map[int64][]string{}
	for _, r := range rows {
		byShow[r.SeriesID] = append(byShow[r.SeriesID], r.Path)
	}
	var out []MisplacedShow
	for id, paths := range byShow {
		m := s.misplacedFrom(paths, roots)
		if len(m) == 0 {
			continue
		}
		noFolder := true
		for _, p := range paths {
			if v := s.fileFolder(roots, p, nil); !v.leftover {
				noFolder = false
				break
			}
		}
		out = append(out, MisplacedShow{SeriesID: id, Files: len(m), NoFolder: noFolder})
	}
	for i := range out {
		if sr, err := s.repo.Get(ctx, out[i].SeriesID); err == nil {
			out[i].Title = sr.Title
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Files != out[b].Files {
			return out[a].Files > out[b].Files
		}
		return out[a].Title < out[b].Title
	})
	return out, nil
}

func firstDistinct(whys []string) string {
	seen := map[string]bool{}
	var out []string
	for _, w := range whys {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
		if len(out) == 2 {
			break
		}
	}
	return strings.Join(out, "; ")
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
	return fmt.Sprintf("Can't tell which folder %s lives in (%s), so nothing was imported. Move the show's files back into its own folder and rescan it, then import again.", e.Title, e.Why)
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
	if info.Path != "" {
		if f, ok := s.folders.Owner(ctx, libfolders.MediaSeries, info.Path); ok {
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
	return s.dbJudge(ctx, sr.ID).judge(filepath.Clean(dir)).why
}
