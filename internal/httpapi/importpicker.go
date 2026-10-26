package httpapi

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// The manual-import folder picker: a browser over the import roots, a name search over
// the downloads folders, and (importsuggest.go) the finished downloads that look like the
// title. Manual import used to list only the downloads folder, so a file anywhere else
// couldn't be imported from the page at all.
//
// Everything here stays inside importRoots and goes through the same test as
// checkImportPath (importGuard): the data folder is refused, every path is fully resolved
// before it's compared, and the comparison is on a separator boundary. Entries are vetted
// one by one too, so a symlink inside a root that points out of it is never listed, and
// the walks never follow symlinks at all. Only names, media-file counts and media-file
// sizes leave the server — never anything read from inside a file.

// Bounds on one browse or search, so a share with a million files can't hang the page.
const (
	pickerTimeout     = 10 * time.Second
	pickerReadMax     = 5000 // directory entries read from the folder on screen
	pickerEntriesMax  = 1000 // folders + media files shown for it
	pickerCountDepth  = 3    // how deep a sub-folder's media files are counted
	pickerCountVisits = 2000 // entries one sub-folder's count may look at
	pickerTotalVisits = 50_000
	searchDepth       = 5
	searchResultsMax  = 50
	searchVisitsMax   = 50_000
	searchQueryMin    = 2
)

// pickerMedia is what counts as importable for a kind: video for movies and series, ebook
// and audiobook files for books.
func pickerMedia(kind string) (func(string) bool, bool) {
	switch kind {
	case "movies", "series":
		return library.IsVideoFile, true
	case "books":
		return library.IsBookFile, true
	}
	return nil, false
}

// importRootLabel names a root for the browser's top level and its breadcrumb.
func importRootLabel(r libroots.Root) string {
	names := map[string]string{
		"movies": "Movies", "tv": "TV", "ebooks": "Ebooks", "audiobooks": "Audiobooks", "music": "Music",
		"downloads": "Downloads", "former-downloads": "Former downloads folder",
	}
	name := names[r.Name]
	if name == "" {
		name = r.Name
	}
	if r.Label == "" {
		return name
	}
	if r.Name == "downloads" {
		return r.Label // already "Downloads for …"
	}
	return name + " — " + r.Label
}

type pickerDir struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Media is how many importable files the folder holds, counted a few levels down;
	// More says the count stopped early, so the UI shows "40+". Absent on the top level:
	// counting a whole library would be the slow walk this picker exists to avoid.
	Media *int `json:"media,omitempty"`
	More  bool `json:"more,omitempty"`
	// Label is the root's name on the top level ("Downloads", "Movies — 4K").
	Label string `json:"label,omitempty"`
}

type pickerFile struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type pickerCrumb struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// visibleRoots is the import roots that exist and pass the guard, deduplicated, each
// with its label — the browser's top level and the search's starting points.
func (a *api) visibleRoots(ctx context.Context, guard func(string) error, only func(libroots.Root) bool) []pickerDir {
	var out []pickerDir
	seen := map[string]bool{}
	for _, r := range a.importRootList(ctx) {
		p := strings.TrimSpace(r.Path)
		if p == "" || (only != nil && !only(r)) {
			continue
		}
		clean, err := filepath.Abs(filepath.Clean(p))
		if err != nil || seen[clean] {
			continue
		}
		seen[clean] = true
		if guard(clean) != nil {
			continue
		}
		if fi, err := os.Stat(clean); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, pickerDir{Name: filepath.Base(clean), Path: clean, Label: importRootLabel(r)})
	}
	return out
}

// handleImportBrowse — GET /api/v1/manualimport/folders?kind=&path= lists one folder for
// the manual-import picker: its sub-folders with how many importable files each holds,
// and the importable files in it. No path lists the import roots themselves.
func (a *api) handleImportBrowse(w http.ResponseWriter, r *http.Request) {
	isMedia, ok := pickerMedia(r.URL.Query().Get("kind"))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "kind must be movies, series or books")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	guard := a.importGuard(ctx)
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		a.writeJSON(w, http.StatusOK, map[string]any{
			"path": "", "crumbs": []pickerCrumb{}, "dirs": a.visibleRoots(ctx, guard, nil), "files": []pickerFile{},
		})
		return
	}
	clean, err := a.checkImportPath(ctx, p)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fi, err := os.Stat(clean); err != nil || !fi.IsDir() {
		a.writeError(w, http.StatusBadRequest, "That folder doesn't exist (or isn't a folder)")
		return
	}
	dirs, files, truncated := readPickerFolder(ctx, clean, guard, isMedia)
	resp := map[string]any{
		"path": clean, "crumbs": a.pickerCrumbs(ctx, clean, guard), "dirs": dirs, "files": files, "truncated": truncated,
	}
	if ctx.Err() != nil {
		if r.Context().Err() != nil {
			return // the browser has gone
		}
		resp["truncated"] = true
	}
	a.writeJSON(w, http.StatusOK, resp)
}

// pickerCrumbs is the way down from the root holding clean to clean itself, for the
// breadcrumb. It climbs while the parent still passes the guard, so it ends at the
// outermost root; the root's crumb carries the root's label.
func (a *api) pickerCrumbs(ctx context.Context, clean string, guard func(string) error) []pickerCrumb {
	var up []string
	for cur := clean; len(up) < 64; {
		up = append(up, cur)
		parent := filepath.Dir(cur)
		if parent == cur || guard(parent) != nil {
			break
		}
		cur = parent
	}
	labels := map[string]string{}
	for _, d := range a.visibleRoots(ctx, guard, nil) {
		labels[d.Path] = d.Label
	}
	out := make([]pickerCrumb, 0, len(up))
	for i := len(up) - 1; i >= 0; i-- {
		name := filepath.Base(up[i])
		if l := labels[up[i]]; l != "" && i == len(up)-1 {
			name = l
		}
		out = append(out, pickerCrumb{Name: name, Path: up[i]})
	}
	return out
}

// readPickerFolder reads one folder: sub-folders (with counts) and media files, hidden
// entries left out, every entry vetted by guard. It reads at most pickerReadMax entries
// and returns at most pickerEntriesMax; truncated says there were more.
func readPickerFolder(ctx context.Context, dir string, guard func(string) error, isMedia func(string) bool) ([]pickerDir, []pickerFile, bool) {
	dirs, files := []pickerDir{}, []pickerFile{}
	f, err := os.Open(dir)
	if err != nil {
		return dirs, files, false
	}
	entries, err := f.ReadDir(pickerReadMax)
	truncated := false
	if err == nil && len(entries) == pickerReadMax {
		// There may be more; one more read says for sure.
		if more, _ := f.ReadDir(1); len(more) > 0 {
			truncated = true
		}
	} else if err != nil && !errors.Is(err, io.EOF) {
		truncated = true
	}
	_ = f.Close()
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })

	visits := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			return dirs, files, true
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if len(dirs)+len(files) >= pickerEntriesMax {
			truncated = true
			break
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			// A link is shown only when where it leads is still inside the roots.
			if guard(full) != nil {
				continue
			}
			fi, err := os.Stat(full)
			if err != nil {
				continue
			}
			isDir = fi.IsDir()
		} else if isDir && guard(full) != nil {
			continue // the data folder, when it sits inside a root
		}
		if isDir {
			d := pickerDir{Name: name, Path: full}
			if visits < pickerTotalVisits {
				n, more, used := countMedia(ctx, full, isMedia, guard)
				visits += used
				d.Media, d.More = &n, more
			}
			dirs = append(dirs, d)
			continue
		}
		if !isMedia(name) {
			continue
		}
		fi, err := os.Stat(full)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		files = append(files, pickerFile{Name: name, Path: full, SizeBytes: fi.Size()})
	}
	return dirs, files, truncated
}

// countMedia counts the media files under dir, pickerCountDepth levels down at most and
// looking at pickerCountVisits entries at most (more = it stopped early). It doesn't
// follow symlinks, skips hidden entries, and never enters a folder the guard refuses.
func countMedia(ctx context.Context, dir string, isMedia func(string) bool, guard func(string) error) (n int, more bool, visits int) {
	base := strings.Count(dir, string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != dir {
				return fs.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			more = true
			return fs.SkipAll
		}
		if p == dir {
			return nil
		}
		visits++
		if visits > pickerCountVisits {
			more = true
			return fs.SkipAll
		}
		if strings.HasPrefix(d.Name(), ".") || d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if guard(p) != nil {
				return fs.SkipDir // the data folder, when it sits inside a root
			}
			if strings.Count(p, string(filepath.Separator))-base >= pickerCountDepth {
				more = more || hasEntries(p)
				return fs.SkipDir
			}
			return nil
		}
		if isMedia(d.Name()) {
			n++
		}
		return nil
	})
	return n, more, visits
}

// hasEntries reports whether a folder holds anything at all (one entry read).
func hasEntries(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	e, _ := f.ReadDir(1)
	return len(e) > 0
}

type pickerHit struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Dir       bool   `json:"dir"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	// Where is the root it was found in ("Downloads") and the path below it, for display.
	Where string `json:"where"`
	Rel   string `json:"rel"`
}

// searchWords splits a name or a query into lowercase, accent-folded words.
func searchWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(parser.FoldAccents(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// nameMatches reports whether every query word appears in name ("dune 2021" finds
// "Dune.Part.One.2021.2160p").
func nameMatches(name string, words []string) bool {
	hay := strings.Join(searchWords(name), " ")
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// handleImportSearch — GET /api/v1/manualimport/search?kind=&q= finds folders and media
// files by name in every downloads folder (current and former), a few levels deep, the
// first searchResultsMax of them. Library folders aren't searched: what's there is
// already imported, and they're the big trees.
func (a *api) handleImportSearch(w http.ResponseWriter, r *http.Request) {
	isMedia, ok := pickerMedia(r.URL.Query().Get("kind"))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "kind must be movies, series or books")
		return
	}
	words := searchWords(r.URL.Query().Get("q"))
	if len(strings.Join(words, "")) < searchQueryMin {
		a.writeError(w, http.StatusBadRequest, "Type at least two letters to search")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	guard := a.importGuard(ctx)
	roots := a.visibleRoots(ctx, guard, func(r libroots.Root) bool {
		return r.Name == "downloads" || r.Name == "former-downloads"
	})
	hits, truncated := searchRoots(ctx, roots, words, isMedia, guard)
	if ctx.Err() != nil {
		if r.Context().Err() != nil {
			return
		}
		truncated = true
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"results": hits, "truncated": truncated})
}

// searchRoots walks each root (no symlinks, no hidden entries, searchDepth levels) for
// folders and media files whose names hold every word.
func searchRoots(ctx context.Context, roots []pickerDir, words []string, isMedia func(string) bool, guard func(string) error) ([]pickerHit, bool) {
	hits := []pickerHit{}
	visits := 0
	truncated := false
	for _, root := range roots {
		base := strings.Count(root.Path, string(filepath.Separator))
		_ = filepath.WalkDir(root.Path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() && p != root.Path {
					return fs.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				truncated = true
				return fs.SkipAll
			}
			if p == root.Path {
				return nil
			}
			visits++
			if visits > searchVisitsMax {
				truncated = true
				return fs.SkipAll
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || d.Type()&fs.ModeSymlink != 0 {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			depth := strings.Count(p, string(filepath.Separator)) - base
			if d.IsDir() && guard(p) != nil {
				return fs.SkipDir
			}
			if (d.IsDir() || isMedia(name)) && nameMatches(name, words) {
				if len(hits) >= searchResultsMax {
					truncated = true
					return fs.SkipAll
				}
				h := pickerHit{Name: name, Path: p, Dir: d.IsDir(), Where: root.Label}
				if rel, err := filepath.Rel(root.Path, p); err == nil {
					h.Rel = filepath.ToSlash(rel)
				}
				if !d.IsDir() {
					if fi, err := d.Info(); err == nil {
						h.SizeBytes = fi.Size()
					}
				}
				hits = append(hits, h)
			}
			if d.IsDir() && depth >= searchDepth {
				return fs.SkipDir
			}
			return nil
		})
		if truncated {
			break
		}
	}
	return hits, truncated
}
