package series

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// FolderYears decides whether a NEW show folder needs the show's year, for the "Add the
// year only when it's needed" naming setting (library.SeriesNaming.YearWhenNeeded).
//
// Sonarr users are used to this from TheTVDB, which writes the year into the names of shows
// that share a title: "Dark Matter (2024)" next to "Dark Matter". TMDB's titles never carry
// a year, so Arrmada works it out. The year is needed when any of these hold for show X
// titled T:
//
//	(a) another series in the library has T as its title;
//	(b) a folder named T (with or without a year or id tag) sits directly in a TV folder
//	    and isn't X's own folder — an untracked show counts;
//	(c) TMDB lists another show titled T.
//
// Titles compare by parser.TitleKey with a leading "The" folded away, so case,
// punctuation, "&"/"and" and "The" never make two titles look different. The cheap,
// certain checks (a) and (b) run first; (c) only when they found nothing. Anything that
// can't be answered — TMDB not set up, down or slow — keeps the year: a year that wasn't
// needed costs nothing, a missing one can put two shows in one folder.
//
// It's only asked when a show has no folder yet. Once it has one, that folder is used as
// it is (imports and renames alike), so this decides a show's folder exactly once.
type FolderYears struct {
	svc *Service
	now func() time.Time

	mu sync.Mutex
	// decided remembers each answer for a while, so every episode of a season pack lands
	// in the same folder: the first episode makes the folder "T", and without this the
	// second could see it as somebody else's, or find TMDB answering differently.
	decided map[string]folderYearMemo
	// tmdb caches TMDB's answer per title; only answers are cached, never failures.
	tmdb map[string]tmdbTitleMemo
}

type folderYearMemo struct {
	needed bool
	at     time.Time
}

type tmdbTitleMemo struct {
	results []metadata.SeriesResult
	at      time.Time
}

const (
	folderYearMemoTTL = 30 * time.Minute
	tmdbTitleMemoTTL  = 6 * time.Hour
	folderYearTimeout = 10 * time.Second
)

// NewFolderYears is the year-when-needed check over the series library svc keeps.
func NewFolderYears(svc *Service) *FolderYears {
	return &FolderYears{svc: svc, now: time.Now, decided: map[string]folderYearMemo{}, tmdb: map[string]tmdbTitleMemo{}}
}

// Needed reports whether a new folder for the show called title (of year) needs the year.
func (y *FolderYears) Needed(ctx context.Context, title string, year int) bool {
	key := folderTitleKey(title)
	if y == nil || y.svc == nil || key == "" || year <= 0 {
		return true
	}
	memoKey := key + "|" + strconv.Itoa(year)
	y.mu.Lock()
	if m, ok := y.decided[memoKey]; ok && y.now().Sub(m.at) < folderYearMemoTTL {
		y.mu.Unlock()
		return m.needed
	}
	y.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, folderYearTimeout)
	defer cancel()
	all, err := y.svc.repo.List(ctx)
	if err != nil {
		return true // can't see the library: keep the year
	}
	q := folderYearQuery{Title: title, Year: year}
	if self, ok := folderYearSelf(all, title, year); ok {
		q.SelfID, q.SelfTMDB = self.ID, self.TMDBID
		q.OwnFolder = y.svc.FolderPath(ctx, self.ID)
	}
	var search func(context.Context, string) ([]metadata.SeriesResult, error)
	if y.svc.meta != nil && y.svc.meta.Available() {
		search = y.searchTMDB
	}
	needed, why := folderYearNeeded(ctx, q, all, y.svc.libRoots(), search)

	y.mu.Lock()
	y.decided[memoKey] = folderYearMemo{needed: needed, at: y.now()}
	y.mu.Unlock()
	if y.svc.log != nil {
		if needed {
			y.svc.log.Info("series: a new show folder keeps its year", "series", title, "year", year, "why", why)
		} else {
			y.svc.log.Info("series: a new show folder leaves out its year — nothing shares its title", "series", title, "year", year)
		}
	}
	return needed
}

// searchTMDB is the metadata provider's series search, cached per title.
func (y *FolderYears) searchTMDB(ctx context.Context, title string) ([]metadata.SeriesResult, error) {
	k := strings.ToLower(strings.TrimSpace(title))
	y.mu.Lock()
	if m, ok := y.tmdb[k]; ok && y.now().Sub(m.at) < tmdbTitleMemoTTL {
		y.mu.Unlock()
		return m.results, nil
	}
	y.mu.Unlock()
	res, err := y.svc.meta.SearchSeries(ctx, title)
	if err != nil {
		return nil, err
	}
	y.mu.Lock()
	y.tmdb[k] = tmdbTitleMemo{results: res, at: y.now()}
	y.mu.Unlock()
	return res, nil
}

// folderYearQuery is the show a new folder is being named for. SelfID/SelfTMDB/OwnFolder
// are zero when the show isn't (yet) in the library or has no folder.
type folderYearQuery struct {
	Title     string
	Year      int
	SelfID    int64
	SelfTMDB  int
	OwnFolder string // the show's own folder on disk, full path
}

// folderYearSelf is the library show a name and year mean: same title, same year.
func folderYearSelf(all []Series, title string, year int) (Series, bool) {
	key := parser.TitleKey(title)
	for _, s := range all {
		if s.Year == year && parser.TitleKey(s.Title) == key {
			return s, true
		}
	}
	return Series{}, false
}

// folderYearNeeded is the decision itself (see FolderYears), with everything it reads
// passed in: the library, the TV folders and TMDB's search (nil = TMDB isn't available).
// why says which rule asked for the year.
func folderYearNeeded(ctx context.Context, q folderYearQuery, library []Series, roots []string,
	search func(context.Context, string) ([]metadata.SeriesResult, error)) (needed bool, why string) {
	key := folderTitleKey(q.Title)
	if key == "" || q.Year <= 0 {
		return true, "no title or year to compare"
	}
	// (a) Another series in the library with the same title.
	for _, s := range library {
		if q.SelfID != 0 && s.ID == q.SelfID {
			continue
		}
		if q.SelfID == 0 && s.Year == q.Year && parser.TitleKey(s.Title) == parser.TitleKey(q.Title) {
			continue // the show itself, before it has an id here
		}
		if folderTitleKey(s.Title) == key {
			return true, "another show in the library is called " + s.Title
		}
	}
	// (b) A folder with that title in a TV folder that isn't the show's own.
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if folderTitleKey(e.Name()) != key {
				continue
			}
			p := filepath.Join(root, e.Name())
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() { // follows a symlinked folder
				continue
			}
			if q.OwnFolder != "" && sameFolderYearPath(p, q.OwnFolder) {
				continue
			}
			return true, "a folder named " + e.Name() + " is already in " + root
		}
	}
	// (c) TMDB knows another show by that title. Not being able to ask keeps the year.
	if search == nil {
		return true, "TMDB isn't available to check for another show with that title"
	}
	results, err := search(ctx, q.Title)
	if err != nil {
		return true, "TMDB couldn't be asked about another show with that title: " + err.Error()
	}
	for _, r := range results {
		if folderTitleKey(r.Title) != key {
			continue
		}
		if q.SelfTMDB > 0 && r.TMDBID == q.SelfTMDB {
			continue
		}
		if q.SelfTMDB <= 0 && r.Year == q.Year {
			continue // most likely the show itself; and a same-year twin can't be told apart by year anyway
		}
		return true, "TMDB lists another show called " + r.Title
	}
	return false, ""
}

// folderTitleKey is the title key two shows are compared by here: parser.TitleKey (case,
// punctuation, accents, "&"/"and", and a trailing "(2024)" or "{tvdb-…}" folded away)
// with a leading "The" dropped too, so "The Office" and "Office" count as one title.
func folderTitleKey(title string) string {
	t := strings.TrimSpace(title)
	if len(t) > 4 && strings.EqualFold(t[:4], "the ") {
		t = t[4:]
	}
	return parser.TitleKey(t)
}

// sameFolderYearPath reports whether two paths name the same folder: the same spelling, or
// the same folder on disk (a case-insensitive share, a symlink). Spellings differing only
// in case are different folders on a case-sensitive disk, so they aren't folded here.
func sameFolderYearPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}
