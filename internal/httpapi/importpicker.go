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
	"sync"
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
// one by one too, so a symlink inside a root that points out of it is never listed. A
// walk starts from its folder resolved (so a downloads folder that is itself a link is
// walked) and never follows a symlink below that. Only names, media-file counts and
// media-file sizes leave the server — never anything read from inside a file.

// pickerTimeout bounds one browse, search or suggestion request. A variable so a test
// can run one against a deadline that has already passed.
var pickerTimeout = 10 * time.Second

// Bounds on one browse or search, so a share with a million files can't hang the page.
const (
	pickerReadMax     = 5000 // directory entries read from the folder on screen
	pickerEntriesMax  = 1000 // folders + media files shown for it
	pickerCountDepth  = 3    // how deep a sub-folder's media files are counted
	pickerCountVisits = 2000 // entries one sub-folder's count may look at
	pickerTotalVisits = 50_000
	pickerReadChunk   = 256 // entries one directory read takes, between deadline checks
	pickerConcurrent  = 2   // picker walks running at once, per server
	searchDepth       = 5
	searchResultsMax  = 50
	searchVisitsMax   = 50_000
	searchQueryMin    = 2
)

// pickerKind is what counts as importable for one kind of title: the very test that
// kind's manual-import listing applies (library.Listed*), so a folder said to hold "2
// videos" opens on 2 — samples and stubs under the size floor aren't counted. name is a
// cheap first check, so a file whose extension can't match is never stat'ed.
type pickerKind struct {
	name   func(string) bool
	listed func(string, int64) bool
}

func pickerMedia(kind string) (pickerKind, bool) {
	switch kind {
	case "movies":
		return pickerKind{library.IsVideoFile, library.ListedMovieVideo}, true
	case "series":
		return pickerKind{library.IsVideoFile, library.ListedEpisodeVideo}, true
	case "books":
		return pickerKind{library.IsBookFile, library.ListedBookFile}, true
	}
	return pickerKind{}, false
}

// file reports whether the entry e at p (not a symlink) is a file the listing would show,
// and its size.
func (k pickerKind) file(p string, e fs.DirEntry) (int64, bool) {
	if !k.name(e.Name()) {
		return 0, false
	}
	fi, err := e.Info()
	if err != nil || !fi.Mode().IsRegular() {
		return 0, false
	}
	return fi.Size(), k.listed(p, fi.Size())
}

// pickerGate lets pickerConcurrent picker walks run at once; the rest are told to try
// again, so a few clicks on a slow share can't stack up walks. The zero value is ready.
type pickerGate struct {
	once  sync.Once
	slots chan struct{}
}

func (g *pickerGate) enter() bool {
	g.once.Do(func() { g.slots = make(chan struct{}, pickerConcurrent) })
	select {
	case g.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *pickerGate) leave() { <-g.slots }

const pickerBusyMessage = "Arrmada is already looking through folders for someone — try again in a moment."

// enterPicker takes a picker slot, or answers 503 and returns false. The caller calls
// a.picker.leave() when it took one.
func (a *api) enterPicker(w http.ResponseWriter) bool {
	if a.picker.enter() {
		return true
	}
	w.Header().Set("Retry-After", "2")
	a.writeError(w, http.StatusServiceUnavailable, pickerBusyMessage)
	return false
}

// readDirChunk reads the next entries of an open folder. Every picker walk reads through
// it; a test swaps it to count what a walk read.
var readDirChunk = func(f *os.File, n int) ([]fs.DirEntry, error) { return f.ReadDir(n) }

// walkBudget is what one walk (or a request's walks) may spend: the request's deadline
// and a cap on entries looked at. cut says either stopped it.
type walkBudget struct {
	ctx    context.Context
	max    int // 0 = no cap
	visits int
	cut    bool
}

func (b *walkBudget) done() bool {
	if !b.cut && b.ctx.Err() != nil {
		b.cut = true
	}
	return b.cut
}

// walkVisit sees one entry (never a hidden one) at depth (1 = in the walk's root).
// enter asks to walk into it — honoured only for a real folder, never a symlink, and
// only above maxDepth; stop ends the whole walk.
type walkVisit func(p string, e fs.DirEntry, depth int) (enter, stop bool)

// boundedWalk walks the tree below root depth first. Unlike filepath.WalkDir it never
// reads or sorts a whole folder up front: each folder is read pickerReadChunk entries at
// a time, the deadline is checked before every read (before the root's too), and the cap
// before every entry. root should already be resolved (resolveWalkRoot); below it,
// symlinks are handed to visit but never followed.
func boundedWalk(b *walkBudget, root string, maxDepth int, visit walkVisit) {
	type frame struct {
		dir   string
		depth int
	}
	stack := []frame{{root, 0}}
	for len(stack) > 0 {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sub, stop := walkFolder(b, fr.dir, fr.depth, maxDepth, visit)
		if stop {
			return
		}
		for i := len(sub) - 1; i >= 0; i-- { // first-read sub-folder walked first
			stack = append(stack, frame{sub[i], fr.depth + 1})
		}
	}
}

// walkFolder reads one folder for boundedWalk, returning the sub-folders to walk next.
func walkFolder(b *walkBudget, dir string, depth, maxDepth int, visit walkVisit) (sub []string, stop bool) {
	if b.done() {
		return nil, true
	}
	f, err := os.Open(dir)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	for {
		if b.done() {
			return sub, true
		}
		entries, err := readDirChunk(f, pickerReadChunk)
		for _, e := range entries {
			if b.max > 0 && b.visits >= b.max {
				b.cut = true
				return sub, true
			}
			b.visits++
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			enter, halt := visit(p, e, depth+1)
			if halt {
				return sub, true
			}
			if enter && e.IsDir() && e.Type()&fs.ModeSymlink == 0 && depth+1 < maxDepth {
				sub = append(sub, p)
			}
		}
		if err != nil || len(entries) == 0 {
			return sub, false
		}
	}
}

// resolveWalkRoot is the real folder a walk of p starts from: p with its symlinks
// resolved — a downloads folder linked into place is walked where it really is — and
// still put through the guard, so the walk's start can't be anywhere a path couldn't be.
func resolveWalkRoot(p string, guard func(string) error) (string, bool) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil || guard(real) != nil {
		return "", false
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", false
	}
	return real, true
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
	kind, ok := pickerMedia(r.URL.Query().Get("kind"))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "kind must be movies, series or books")
		return
	}
	if !a.enterPicker(w) {
		return
	}
	defer a.picker.leave()
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
	dirs, files, truncated := readPickerFolder(ctx, clean, guard, kind)
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
func readPickerFolder(ctx context.Context, dir string, guard func(string) error, kind pickerKind) ([]pickerDir, []pickerFile, bool) {
	dirs, files := []pickerDir{}, []pickerFile{}
	if ctx.Err() != nil {
		return dirs, files, true
	}
	f, err := os.Open(dir)
	if err != nil {
		return dirs, files, false
	}
	entries, err := readDirChunk(f, pickerReadMax)
	truncated := false
	if err == nil && len(entries) == pickerReadMax {
		// There may be more; one more read says for sure.
		if more, _ := readDirChunk(f, 1); len(more) > 0 {
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
		link := e.Type()&fs.ModeSymlink != 0
		isDir := e.IsDir()
		var linked os.FileInfo
		if link {
			// A link is shown only when where it leads is still inside the roots.
			if guard(full) != nil {
				continue
			}
			fi, err := os.Stat(full)
			if err != nil {
				continue
			}
			isDir, linked = fi.IsDir(), fi
		} else if isDir && guard(full) != nil {
			continue // the data folder, when it sits inside a root
		}
		if isDir {
			d := pickerDir{Name: name, Path: full}
			if visits < pickerTotalVisits {
				if real, ok := resolveWalkRoot(full, guard); ok {
					n, more, used := countMedia(ctx, real, kind, guard)
					visits += used
					d.Media, d.More = &n, more
				}
			}
			dirs = append(dirs, d)
			continue
		}
		var size int64
		if link {
			if !kind.name(name) || !linked.Mode().IsRegular() || !kind.listed(full, linked.Size()) {
				continue
			}
			size = linked.Size()
		} else {
			s, ok := kind.file(full, e)
			if !ok {
				continue
			}
			size = s
		}
		files = append(files, pickerFile{Name: name, Path: full, SizeBytes: size})
	}
	return dirs, files, truncated
}

// countMedia counts the files the listing would show under dir (already resolved),
// pickerCountDepth levels down at most and looking at pickerCountVisits entries at most
// (more = it stopped early). It doesn't follow symlinks, skips hidden entries, and never
// enters a folder the guard refuses.
func countMedia(ctx context.Context, dir string, kind pickerKind, guard func(string) error) (n int, more bool, visits int) {
	b := &walkBudget{ctx: ctx, max: pickerCountVisits}
	boundedWalk(b, dir, pickerCountDepth, func(p string, e fs.DirEntry, depth int) (bool, bool) {
		if e.Type()&fs.ModeSymlink != 0 {
			return false, false
		}
		if e.IsDir() {
			if guard(p) != nil {
				return false, false // the data folder, when it sits inside a root
			}
			if depth >= pickerCountDepth {
				more = more || hasEntries(p)
				return false, false
			}
			return true, false
		}
		if _, ok := kind.file(p, e); ok {
			n++
		}
		return false, false
	})
	return n, more || b.cut, b.visits
}

// hasEntries reports whether a folder holds anything at all (one entry read).
func hasEntries(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	e, _ := readDirChunk(f, 1)
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
	root  int    // which root, for the sort
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
	kind, ok := pickerMedia(r.URL.Query().Get("kind"))
	if !ok {
		a.writeError(w, http.StatusBadRequest, "kind must be movies, series or books")
		return
	}
	words := searchWords(r.URL.Query().Get("q"))
	if len(strings.Join(words, "")) < searchQueryMin {
		a.writeError(w, http.StatusBadRequest, "Type at least two letters to search")
		return
	}
	if !a.enterPicker(w) {
		return
	}
	defer a.picker.leave()
	ctx, cancel := context.WithTimeout(r.Context(), pickerTimeout)
	defer cancel()
	guard := a.importGuard(ctx)
	roots := a.visibleRoots(ctx, guard, func(r libroots.Root) bool {
		return r.Name == "downloads" || r.Name == "former-downloads"
	})
	hits, truncated := searchRoots(ctx, roots, words, kind, guard)
	if ctx.Err() != nil {
		if r.Context().Err() != nil {
			return
		}
		truncated = true
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"results": hits, "truncated": truncated})
}

// searchRoots walks each root (resolved; no symlinks below it, no hidden entries,
// searchDepth levels, searchVisitsMax entries in all) for folders and media files whose
// names hold every word.
func searchRoots(ctx context.Context, roots []pickerDir, words []string, kind pickerKind, guard func(string) error) ([]pickerHit, bool) {
	hits := []pickerHit{}
	b := &walkBudget{ctx: ctx, max: searchVisitsMax}
	full := false
	for ri, root := range roots {
		real, ok := resolveWalkRoot(root.Path, guard)
		if !ok {
			continue
		}
		boundedWalk(b, real, searchDepth, func(p string, e fs.DirEntry, _ int) (bool, bool) {
			if e.Type()&fs.ModeSymlink != 0 {
				return false, false
			}
			if e.IsDir() && guard(p) != nil {
				return false, false
			}
			if !nameMatches(e.Name(), words) {
				return e.IsDir(), false
			}
			h := pickerHit{Name: e.Name(), Path: p, Dir: e.IsDir(), Where: root.Label, root: ri}
			if !e.IsDir() {
				size, ok := kind.file(p, e)
				if !ok {
					return false, false
				}
				h.SizeBytes = size
			}
			if len(hits) >= searchResultsMax {
				full = true
				return false, true
			}
			if rel, err := filepath.Rel(real, p); err == nil {
				h.Rel = filepath.ToSlash(rel)
			}
			hits = append(hits, h)
			return e.IsDir(), false
		})
		if full || b.cut {
			break
		}
	}
	// The walk reads folders in the order the disk gives; show them in name order, a
	// folder ahead of what's inside it.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].root != hits[j].root {
			return hits[i].root < hits[j].root
		}
		return strings.ToLower(hits[i].Rel) < strings.ToLower(hits[j].Rel)
	})
	return hits, full || b.cut
}
