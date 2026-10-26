package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/books"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/store"
)

func mustKind(t *testing.T, kind string) pickerKind {
	t.Helper()
	k, ok := pickerMedia(kind)
	if !ok {
		t.Fatalf("no picker kind %q", kind)
	}
	return k
}

// countReads makes every picker directory read count the entries it hands back.
func countReads(t *testing.T) *int {
	t.Helper()
	n := new(int)
	orig := readDirChunk
	readDirChunk = func(f *os.File, k int) ([]fs.DirEntry, error) {
		e, err := orig(f, k)
		*n += len(e)
		return e, err
	}
	t.Cleanup(func() { readDirChunk = orig })
	return n
}

// manyFiles fills dir with n empty files.
func manyFiles(t *testing.T, dir string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.mkv", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A walk reads a folder a chunk at a time and stops at its cap: a folder of 3000 entries
// with a cap of 100 is read 100 entries and at most one chunk further, never in full.
func TestBoundedWalkStopsAtTheCapWithoutReadingTheWholeFolder(t *testing.T) {
	dir := t.TempDir()
	manyFiles(t, dir, 3000)
	reads := countReads(t)
	b := &walkBudget{ctx: context.Background(), max: 100}
	seen := 0
	boundedWalk(b, dir, 3, func(string, fs.DirEntry, int) (bool, bool) { seen++; return false, false })
	if seen != 100 || b.visits != 100 || !b.cut {
		t.Errorf("seen %d, visits %d, cut %v; want 100, 100, true", seen, b.visits, b.cut)
	}
	if *reads > 100+pickerReadChunk {
		t.Errorf("read %d entries for a cap of 100", *reads)
	}
}

// Past the deadline a walk reads nothing at all, not even its root.
func TestBoundedWalkReadsNothingAfterTheDeadline(t *testing.T) {
	dir := t.TempDir()
	manyFiles(t, dir, 10)
	reads := countReads(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &walkBudget{ctx: ctx}
	boundedWalk(b, dir, 3, func(string, fs.DirEntry, int) (bool, bool) {
		t.Error("visited an entry after the deadline")
		return false, false
	})
	if *reads != 0 || !b.cut {
		t.Errorf("read %d entries, cut %v", *reads, b.cut)
	}
	// The suggestion scan too: it used to read the root before looking at the clock.
	cfg, _, _ := pickerFixture(t)
	a := &api{deps: Deps{Config: cfg}}
	if got := scanSuggestions(ctx, cfg.DownloadsDir, movieMatcher(movies.Movie{Title: "Loose Film", Year: 2019}, nil), mustKind(t, "movies"), a.importGuard(context.Background())); len(got) != 0 || *reads != 0 {
		t.Errorf("scan after the deadline: %+v, read %d", got, *reads)
	}
}

// One sub-folder's count stops at pickerCountVisits and says so, reading about that much.
func TestCountMediaIsBounded(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	manyFiles(t, filepath.Join(cfg.DownloadsDir, "Huge"), pickerCountVisits+1000)
	reads := countReads(t)
	_, got, body := browse(t, srv, cfg.DownloadsDir)
	var huge *pickerDir
	for i := range got.Dirs {
		if got.Dirs[i].Name == "Huge" {
			huge = &got.Dirs[i]
		}
	}
	if huge == nil || !huge.More {
		t.Fatalf("Huge = %+v, want a count marked cut short (%s)", huge, body)
	}
	// The folder on screen, the other folders' counts, and Huge's capped read.
	if *reads > pickerCountVisits+pickerReadChunk+100 {
		t.Errorf("read %d entries", *reads)
	}
}

// A search past its deadline answers at once, cut short, having read nothing.
func TestImportSearchStopsAtTheDeadline(t *testing.T) {
	_, _, srv := pickerFixture(t)
	old := pickerTimeout
	pickerTimeout = 0
	t.Cleanup(func() { pickerTimeout = old })
	reads := countReads(t)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/manualimport/search?kind=movies&q=dune", "", auth.RoleManager))
	var out struct {
		Results   []pickerHit `json:"results"`
		Truncated bool        `json:"truncated"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Body.String())
	}
	if len(out.Results) != 0 || !out.Truncated || *reads != 0 {
		t.Errorf("results %+v, truncated %v, read %d", out.Results, out.Truncated, *reads)
	}
}

// Two picker walks run at once; a third is told to try again rather than queued.
func TestImportPickerBusy(t *testing.T) {
	cfg, _, _ := pickerFixture(t)
	a := &api{deps: Deps{Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	for i := 0; i < pickerConcurrent; i++ {
		if !a.picker.enter() {
			t.Fatalf("slot %d refused", i)
		}
	}
	call := func(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest("GET", target, nil))
		return rec
	}
	for _, c := range []struct {
		h      http.HandlerFunc
		target string
	}{
		{a.handleImportSearch, "/?kind=movies&q=dune"},
		{a.handleImportBrowse, "/?kind=movies&path=" + url.QueryEscape(cfg.DownloadsDir)},
	} {
		rec := call(c.h, c.target)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "try again") || rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s while busy: HTTP %d %s", c.target, rec.Code, rec.Body.String())
		}
	}
	a.picker.leave()
	if rec := call(a.handleImportSearch, "/?kind=movies&q=dune"); rec.Code != 200 {
		t.Errorf("with a slot free: HTTP %d %s", rec.Code, rec.Body.String())
	}
}

// Every picker route is staff-only. Real titles exist, so a refusal can only be the role
// check: readonly and requester get 403, a manager gets the answer.
func TestImportPickerRoutesNeedManager(t *testing.T) {
	cfg, _, _ := pickerFixture(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := st.DB()
	for _, q := range []string{
		`INSERT INTO movies (id, tmdb_id, title, year, monitored, min_availability) VALUES (1, 1, 'Dune: Part One', 2021, 1, 'announced')`,
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 1, 'Severance', 1)`,
		`INSERT INTO books (id, ol_key, title) VALUES (1, 'OL1W', 'Children of Dune')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(Deps{
		Config: cfg, Log: log, Store: st,
		Movies: movies.NewService(db, nil, nil, t.TempDir(), "", nil, log),
		Series: series.NewService(db, nil, t.TempDir(), log),
		Books:  books.NewService(db, nil, log),
	})
	for _, target := range []string{
		"/api/v1/manualimport/folders?kind=movies&path=" + url.QueryEscape(cfg.DownloadsDir),
		"/api/v1/manualimport/search?kind=movies&q=dune",
		"/api/v1/movies/1/manualimport/suggestions",
		"/api/v1/series/1/manualimport/suggestions",
		"/api/v1/books/1/manualimport/suggestions",
	} {
		for role, want := range map[auth.Role]int{
			auth.RoleReadonly:  http.StatusForbidden,
			auth.RoleRequester: http.StatusForbidden,
			auth.RoleManager:   http.StatusOK,
		} {
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, lanRequest("GET", target, "", role))
			if rec.Code != want {
				t.Errorf("%s as %s: HTTP %d, want %d (%s)", target, role, rec.Code, want, rec.Body.String())
			}
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/manualimport/folders?kind=music", "", auth.RoleManager))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: HTTP %d", rec.Code)
	}
	// The movie's suggestion is the matching download.
	rec = httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/movies/1/manualimport/suggestions", "", auth.RoleManager))
	if !strings.Contains(rec.Body.String(), "Dune.Part.One.2021.2160p") {
		t.Errorf("movie suggestions: %s", rec.Body.String())
	}
}

// A downloads folder that is itself a symlink (a share linked into place) is walked: the
// root is resolved before the walk, and still has to pass the guard. A symlinked
// sub-folder that stays inside the roots is counted the same way.
func TestImportPickerSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-downloads")
	cfg := config.Config{
		DownloadsDir: filepath.Join(base, "downloads"),
		MoviesDir:    filepath.Join(base, "library", "movies"),
		DataDir:      filepath.Join(base, "data"),
	}
	sparseFile(t, filepath.Join(real, "Dune.Part.One.2021.2160p", "Dune.Part.One.2021.2160p.mkv"), bigVideo)
	sparseFile(t, filepath.Join(cfg.MoviesDir, "Film (2020)", "Film.2020.mkv"), bigVideo)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, real, cfg.DownloadsDir)
	symlink(t, filepath.Join(cfg.MoviesDir, "Film (2020)"), filepath.Join(real, "linked-film"))
	srv := New(Deps{Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})

	if hits := search(t, srv, "dune"); len(hits) != 2 {
		t.Errorf("search through a symlinked root: %+v, want the folder and its video", hits)
	}
	a := &api{deps: Deps{Config: cfg}}
	if got := a.importSuggestions(context.Background(), movieMatcher(movies.Movie{Title: "Dune: Part One", Year: 2021}, nil), mustKind(t, "movies")); len(got) != 1 {
		t.Errorf("suggestions through a symlinked root: %+v", got)
	}
	code, got, body := browse(t, srv, cfg.DownloadsDir)
	if code != 200 {
		t.Fatalf("HTTP %d %s", code, body)
	}
	counts := map[string]int{}
	for _, d := range got.Dirs {
		if d.Media != nil {
			counts[d.Name] = *d.Media
		}
	}
	if counts["Dune.Part.One.2021.2160p"] != 1 || counts["linked-film"] != 1 {
		t.Errorf("counts = %v, want 1 video in each", counts)
	}
}

// A folder's count is what the listing will show for that kind: the series listing drops
// samples by name, every video listing drops anything under the size floor, and the book
// listing drops samples.
func TestImportBrowseCountsMatchTheListing(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	show := filepath.Join(cfg.DownloadsDir, "Show.S01.1080p")
	sparseFile(t, filepath.Join(show, "Show.S01E01.mkv"), bigVideo)
	sparseFile(t, filepath.Join(show, "Show.S01E02.mkv"), bigVideo)
	sparseFile(t, filepath.Join(show, "Show.S01.sample.mkv"), bigVideo)
	sparseFile(t, filepath.Join(show, "Show.S01E03.stub.mkv"), 10)
	book := filepath.Join(cfg.DownloadsDir, "Book.EPUB")
	sparseFile(t, filepath.Join(book, "Book.epub"), 10)
	sparseFile(t, filepath.Join(book, "Book.sample.epub"), 10)

	count := func(kind, name string) int {
		t.Helper()
		_, got, body := browseKind(t, srv, kind, cfg.DownloadsDir)
		for _, d := range got.Dirs {
			if d.Name == name && d.Media != nil {
				return *d.Media
			}
		}
		t.Fatalf("%s not listed: %s", name, body)
		return -1
	}
	vids, _, _ := library.FindVideosCtx(context.Background(), show, 0, 0)
	if got := count("series", "Show.S01.1080p"); got != 2 || got != len(vids) {
		t.Errorf("series count %d, listing %d, want 2", got, len(vids))
	}
	// The movie listing takes any full-size video, a sample-named one included.
	if got := count("movies", "Show.S01.1080p"); got != 3 {
		t.Errorf("movies count %d, want 3", got)
	}
	bks, _, _ := library.FindBookFilesCtx(context.Background(), book, 0, 0)
	if got := count("books", "Book.EPUB"); got != 1 || got != len(bks) {
		t.Errorf("books count %d, listing %d, want 1", got, len(bks))
	}
}
