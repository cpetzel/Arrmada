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
	k, _ := pickerMedia(kind)
	if !a.enterPicker(w) {
		return
	}
	defer a.picker.leave()
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	out := a.importSuggestions(ctx, match, k)
	if r.Context().Err() != nil {
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

// vetSuggestion is the last check on anything offered, wherever it came from: the
// cleaned path must pass the import guard (so a client's path outside the roots, or a
// link leading out of them, is never offered) and must exist where Arrmada can see it.
func vetSuggestion(guard func(string) error, p string) (string, bool) {
	clean, err := filepath.Abs(filepath.Clean(p))
	if err != nil || guard(clean) != nil {
		return "", false
	}
	if _, err := os.Stat(clean); err != nil {
		return "", false // the client's path isn't one Arrmada can see
	}
	return clean, true
}

// importSuggestions gathers the matches: client downloads first (they carry a hash, so
// "imported before" and "not managed" can be said), then whatever else sits in the
// downloads folders. The best match comes first, the newest among equals.
func (a *api) importSuggestions(ctx context.Context, match suggestMatcher, kind pickerKind) []importSuggestion {
	guard := a.importGuard(ctx)
	out := []importSuggestion{}
	seen := map[string]bool{}
	add := func(s importSuggestion) {
		clean, ok := vetSuggestion(guard, s.Path)
		if !ok || seen[clean] {
			return
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
		for _, s := range scanSuggestions(ctx, root.Path, match, kind, guard) {
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
// category sub-folders such as downloads/movies) for names that match: folders, and files
// the listing would show. The folder is resolved first (a downloads folder linked into
// place is scanned); below it, symlinks are skipped, not followed or offered. No hidden
// entries, at most suggestScanVisits entries, and nothing read once the deadline passes.
func scanSuggestions(ctx context.Context, root string, match suggestMatcher, kind pickerKind, guard func(string) error) []importSuggestion {
	var out []importSuggestion
	real, ok := resolveWalkRoot(root, guard)
	if !ok {
		return nil
	}
	b := &walkBudget{ctx: ctx, max: suggestScanVisits}
	boundedWalk(b, real, 2, func(p string, e fs.DirEntry, _ int) (bool, bool) {
		if e.Type()&fs.ModeSymlink != 0 {
			return false, false
		}
		if e.IsDir() && guard(p) != nil {
			return false, false
		}
		sc := match(e.Name())
		if sc == 0 {
			return e.IsDir(), false
		}
		s := importSuggestion{Name: e.Name(), Path: p, score: sc}
		if !e.IsDir() {
			size, ok := kind.file(p, e)
			if !ok {
				return false, false
			}
			s.SizeBytes = size
		}
		if fi, err := e.Info(); err == nil {
			s.AddedOn = fi.ModTime().Unix()
		}
		out = append(out, s)
		return false, false // a match's own contents are its files, not more matches
	})
	return out
}
