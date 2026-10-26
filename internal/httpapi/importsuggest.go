package httpapi

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Suggested downloads for manual import: "I grabbed it, now import it". The finished
// downloads in the clients' queues (Arrmada's own and Not managed ones alike) and the
// folders and files sitting at the top of the downloads folders, kept when their name
// reads as this title by the same title keys the importer matches with. Each one passes
// the import guard before it's offered, so a client path Arrmada can't see (or one
// outside the roots) never shows.

const (
	suggestMax        = 20
	suggestScanVisits = 3000 // entries looked at per downloads folder (top two levels)
)

type importSuggestion struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	// InClient: it's a finished download in a download client; otherwise it was found
	// in a downloads folder. NotManaged: in a client, but nothing in Arrmada grabbed it.
	InClient   bool  `json:"in_client"`
	NotManaged bool  `json:"not_managed,omitempty"`
	Imported   bool  `json:"imported,omitempty"` // imported before (by its hash)
	AddedOn    int64 `json:"added_on,omitempty"`
	score      int
}

// suggestMatcher scores a release or folder name against one title: 0 is no match.
type suggestMatcher func(name string) int

// movieMatcher matches a movie's own, original and alternative titles, with the year
// within one when the name has one — movies.TitleMatches, the importer's own rule.
func movieMatcher(m movies.Movie, taken movies.PrimaryKeys) suggestMatcher {
	keys := movies.TitleKeys(m, taken)
	return func(name string) int {
		rel := parser.Parse(name)
		if !movies.TitleMatches(keys, m, rel.Title, rel.Year) {
			return 0
		}
		if rel.Year != 0 && rel.Year == m.Year {
			return 3
		}
		return 2
	}
}

// seriesMatcher matches the series' title or any of its aliases; a year in the name that
// belongs to the title (Doctor.Who.2005) must be within one of the series' year.
func seriesMatcher(title string, year int, aliases []string) suggestMatcher {
	keys := map[string]bool{}
	for _, t := range append([]string{title}, aliases...) {
		if k := parser.TitleKey(t); k != "" {
			keys[k] = true
		}
	}
	return func(name string) int {
		rel := parser.Parse(name)
		if !keys[parser.TitleKey(rel.Title)] {
			return 0
		}
		if rel.TitleYear != 0 && year != 0 {
			d := rel.TitleYear - year
			if d < -1 || d > 1 {
				return 0
			}
			return 3
		}
		return 2
	}
}

// bookMatcher matches a name that holds the book's title (book releases are "Author -
// Title (Year) [EPUB]" and every other order). A short title must be the whole parsed
// title, or "It" would match everything; the author's name in it ranks it higher.
func bookMatcher(title, author string) suggestMatcher {
	key := parser.TitleKey(title)
	akey := ""
	if words := strings.Fields(author); len(words) > 0 {
		akey = parser.TitleKey(words[len(words)-1]) // the surname
	}
	return func(name string) int {
		if key == "" {
			return 0
		}
		nk := parser.TitleKey(name)
		hit := false
		if len(key) >= 5 {
			hit = strings.Contains(nk, key)
		} else {
			hit = parser.TitleKey(parser.Parse(name).Title) == key
		}
		if !hit {
			return 0
		}
		if len(akey) >= 3 && strings.Contains(nk, akey) {
			return 3
		}
		return 2
	}
}

// handleMovieImportSuggestions, handleSeriesImportSuggestions, handleBookImportSuggestions
// — GET /api/v1/{movies,series,books}/{id}/manualimport/suggestions.
func (a *api) handleMovieImportSuggestions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	m, err := a.deps.Movies.Get(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "movie not found")
		return
	}
	taken, _ := a.deps.Movies.PrimaryKeys(r.Context())
	a.writeSuggestions(w, r, "movies", movieMatcher(m, taken))
}

func (a *api) handleSeriesImportSuggestions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	sr, err := a.deps.Series.Get(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "series not found")
		return
	}
	var aliases []string
	for _, al := range a.deps.Series.Aliases(r.Context(), id) {
		aliases = append(aliases, al.Title)
	}
	a.writeSuggestions(w, r, "series", seriesMatcher(sr.Title, sr.Year, aliases))
}

func (a *api) handleBookImportSuggestions(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	b, err := a.deps.Books.Get(r.Context(), id)
	if err != nil {
		a.writeError(w, http.StatusNotFound, "book not found")
		return
	}
	a.writeSuggestions(w, r, "books", bookMatcher(b.Title, b.Author))
}

func (a *api) writeSuggestions(w http.ResponseWriter, r *http.Request, kind string, match suggestMatcher) {
	isMedia, _ := pickerMedia(kind)
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	out := a.importSuggestions(ctx, match, isMedia)
	if r.Context().Err() != nil {
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// importSuggestions gathers the matches: client downloads first (they carry a hash, so
// "imported before" and "not managed" can be said), then whatever else sits in the
// downloads folders. The best match comes first, the newest among equals.
func (a *api) importSuggestions(ctx context.Context, match suggestMatcher, isMedia func(string) bool) []importSuggestion {
	guard := a.importGuard(ctx)
	out := []importSuggestion{}
	seen := map[string]bool{}
	add := func(s importSuggestion) {
		clean, err := filepath.Abs(filepath.Clean(s.Path))
		if err != nil || seen[clean] || guard(clean) != nil {
			return
		}
		if _, err := os.Stat(clean); err != nil {
			return // the client's path isn't one Arrmada can see
		}
		seen[clean] = true
		s.Path = clean
		out = append(out, s)
	}

	if a.deps.Downloads != nil {
		if snap, err := a.deps.Downloads.Snapshot(ctx); err == nil {
			var grabbed map[string]bool
			if a.deps.Automation != nil {
				grabbed, _ = a.deps.Automation.GrabbedHashes(ctx)
			}
			type hit struct {
				s    importSuggestion
				hash string
			}
			var hits []hit
			hashes := []string{}
			for _, it := range snap.Items {
				if !it.Complete() || strings.TrimSpace(it.ContentPath) == "" {
					continue
				}
				sc := match(it.Name)
				if sc == 0 {
					continue
				}
				h := strings.ToLower(it.Hash)
				s := importSuggestion{Name: it.Name, Path: it.ContentPath, SizeBytes: it.SizeBytes, InClient: true, AddedOn: it.AddedOn, score: sc}
				if grabbed != nil && h != "" && !grabbed[h] {
					s.NotManaged = true
				}
				hits = append(hits, hit{s, it.Hash})
				if it.Hash != "" {
					hashes = append(hashes, it.Hash)
				}
			}
			var imported map[string]bool
			if a.deps.Library != nil && len(hashes) > 0 {
				imported, _ = a.deps.Library.ImportedAmong(ctx, hashes)
			}
			for _, h := range hits {
				h.s.Imported = imported[h.hash] || imported[strings.ToLower(h.hash)]
				add(h.s)
			}
		}
	}

	for _, root := range a.visibleRoots(ctx, guard, func(r libroots.Root) bool {
		return r.Name == "downloads" || r.Name == "former-downloads"
	}) {
		for _, s := range scanSuggestions(ctx, root.Path, match, isMedia) {
			add(s)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].Imported != out[j].Imported {
			return !out[i].Imported // not yet imported first
		}
		return out[i].AddedOn > out[j].AddedOn
	})
	if len(out) > suggestMax {
		out = out[:suggestMax]
	}
	return out
}

// scanSuggestions looks at the top two levels of one downloads folder (the second for
// category sub-folders such as downloads/movies) for names that match. No symlinks, no
// hidden entries, at most suggestScanVisits entries.
func scanSuggestions(ctx context.Context, root string, match suggestMatcher, isMedia func(string) bool) []importSuggestion {
	var out []importSuggestion
	base := strings.Count(root, string(filepath.Separator))
	visits := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		visits++
		if ctx.Err() != nil || visits > suggestScanVisits {
			return fs.SkipAll
		}
		if strings.HasPrefix(d.Name(), ".") || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if sc := match(d.Name()); sc > 0 && (d.IsDir() || isMedia(d.Name())) {
			s := importSuggestion{Name: d.Name(), Path: p, score: sc}
			if fi, err := d.Info(); err == nil {
				s.AddedOn = fi.ModTime().Unix()
				if !d.IsDir() {
					s.SizeBytes = fi.Size()
				}
			}
			out = append(out, s)
			if d.IsDir() {
				return fs.SkipDir // a match's own contents are its files, not more matches
			}
			return nil
		}
		if d.IsDir() && strings.Count(p, string(filepath.Separator))-base >= 2 {
			return fs.SkipDir
		}
		return nil
	})
	return out
}
