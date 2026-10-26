package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// pickerFixture is downloads, a Movies library, the data folder and an "elsewhere" side
// by side (importTestDirs), with a few releases in downloads.
func pickerFixture(t *testing.T) (cfg config.Config, outside string, srv *http.Server) {
	t.Helper()
	cfg, outside = importTestDirs(t)
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg.DownloadsDir, "Dune.Part.One.2021.2160p", "Dune.Part.One.2021.2160p.mkv"))
	write(filepath.Join(cfg.DownloadsDir, "Dune.Part.One.2021.2160p", "Sample", "sample.mkv"))
	write(filepath.Join(cfg.DownloadsDir, "Dune.Part.One.2021.2160p", "release.nfo"))
	write(filepath.Join(cfg.DownloadsDir, "Loose.Film.2019.mkv"))
	write(filepath.Join(cfg.DownloadsDir, "notes.txt"))
	write(filepath.Join(cfg.DownloadsDir, ".hidden", "x.mkv"))
	write(filepath.Join(outside, "Secret.Film.2020.mkv"))
	write(filepath.Join(cfg.DataDir, "arrmada.db"))
	return cfg, outside, New(Deps{Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

type browseResp struct {
	Path   string        `json:"path"`
	Crumbs []pickerCrumb `json:"crumbs"`
	Dirs   []pickerDir   `json:"dirs"`
	Files  []pickerFile  `json:"files"`
}

func browse(t *testing.T, srv *http.Server, path string) (int, browseResp, string) {
	t.Helper()
	target := "/api/v1/manualimport/folders?kind=movies"
	if path != "" {
		target += "&path=" + url.QueryEscape(path)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", target, "", auth.RoleManager))
	var out browseResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, out, rec.Body.String()
}

func dirNames(ds []pickerDir) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return out
}

func TestImportBrowseTopLevelIsTheImportRoots(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	code, got, body := browse(t, srv, "")
	if code != 200 {
		t.Fatalf("HTTP %d %s", code, body)
	}
	paths := map[string]string{}
	for _, d := range got.Dirs {
		paths[d.Path] = d.Label
		if d.Media != nil {
			t.Errorf("%s: the top level counts nothing, got %d", d.Path, *d.Media)
		}
	}
	if paths[filepath.Clean(cfg.DownloadsDir)] != "Downloads" || paths[filepath.Clean(cfg.MoviesDir)] != "Movies" || len(paths) != 2 {
		t.Errorf("roots = %v, want exactly Downloads and Movies", paths)
	}
}

func TestImportBrowseListsFoldersCountsAndMediaFiles(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	code, got, body := browse(t, srv, cfg.DownloadsDir)
	if code != 200 {
		t.Fatalf("HTTP %d %s", code, body)
	}
	if names := dirNames(got.Dirs); strings.Join(names, ",") != "Dune.Part.One.2021.2160p,sub" {
		t.Errorf("dirs = %v (hidden and outside folders must not show)", names)
	}
	if d := got.Dirs[0]; d.Media == nil || *d.Media != 2 || d.More {
		t.Errorf("Dune counted %+v, want 2 videos", d)
	}
	if len(got.Files) != 1 || got.Files[0].Name != "Loose.Film.2019.mkv" || got.Files[0].SizeBytes != 1 {
		t.Errorf("files = %+v, want only the video", got.Files)
	}
	if len(got.Crumbs) != 1 || got.Crumbs[0].Name != "Downloads" {
		t.Errorf("crumbs = %+v", got.Crumbs)
	}
	code, got, _ = browse(t, srv, filepath.Join(cfg.DownloadsDir, "Dune.Part.One.2021.2160p", "Sample"))
	if code != 200 || len(got.Crumbs) != 3 || got.Crumbs[0].Name != "Downloads" || got.Crumbs[2].Name != "Sample" {
		t.Errorf("deep crumbs: %d %+v", code, got.Crumbs)
	}
}

// Every way out of the roots is refused before anything is read.
func TestImportBrowseRefusesEscapes(t *testing.T) {
	cfg, outside, srv := pickerFixture(t)
	lookalike := cfg.DownloadsDir + "-old"
	if err := os.MkdirAll(lookalike, 0o755); err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	for _, p := range []string{
		outside,
		"/",
		cfg.DownloadsDir + sep + ".." + sep + "elsewhere",
		cfg.DownloadsDir + sep + "sub" + sep + ".." + sep + ".." + sep + "..",
		lookalike,
		cfg.DataDir,
		cfg.LibraryDir, // the managed volume isn't a root
		"relative/path",
	} {
		code, _, body := browse(t, srv, p)
		if code != http.StatusBadRequest {
			t.Errorf("browse %q: HTTP %d %s, want 400", p, code, body)
		}
	}
	// The data folder is named as such.
	if _, _, body := browse(t, srv, cfg.DataDir); !strings.Contains(body, "data folder") {
		t.Errorf("data folder refusal: %s", body)
	}
}

// A symlink inside a root that leads out is neither listed, nor counted, nor browsable.
func TestImportBrowseSymlinkOut(t *testing.T) {
	cfg, outside, srv := pickerFixture(t)
	link := filepath.Join(cfg.DownloadsDir, "escape")
	symlink(t, outside, link)
	symlink(t, filepath.Join(outside, "Secret.Film.2020.mkv"), filepath.Join(cfg.DownloadsDir, "Secret.Film.2020.mkv"))
	symlink(t, outside, filepath.Join(cfg.DownloadsDir, "sub", "deeper"))
	code, got, _ := browse(t, srv, cfg.DownloadsDir)
	if code != 200 {
		t.Fatal(code)
	}
	for _, d := range got.Dirs {
		if d.Name == "escape" {
			t.Errorf("listed a symlink that leads out: %+v", d)
		}
		if d.Name == "sub" && (d.Media == nil || *d.Media != 0) {
			t.Errorf("counted through a symlink: %+v", d)
		}
	}
	for _, f := range got.Files {
		if strings.HasPrefix(f.Name, "Secret") {
			t.Errorf("listed a file symlink that leads out: %+v", f)
		}
	}
	if code, _, body := browse(t, srv, link); code != http.StatusBadRequest {
		t.Errorf("browse through the link: HTTP %d %s", code, body)
	}
	// Search doesn't walk through it either.
	if hits := search(t, srv, "secret"); len(hits) != 0 {
		t.Errorf("search found %+v through a symlink", hits)
	}
}

// A symlink that stays inside the roots is fine (a downloads folder linked into place).
func TestImportBrowseSymlinkInsideRootIsListed(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	symlink(t, filepath.Join(cfg.DownloadsDir, "Dune.Part.One.2021.2160p"), filepath.Join(cfg.DownloadsDir, "sub", "dune-link"))
	code, got, _ := browse(t, srv, filepath.Join(cfg.DownloadsDir, "sub"))
	if code != 200 || len(got.Dirs) != 1 || got.Dirs[0].Name != "dune-link" {
		t.Errorf("inside link: %d %+v", code, got.Dirs)
	}
}

// The data folder inside a root is skipped in the listing, the count, the search and the
// suggestions, and refused when asked for directly.
func TestImportPickerDataDirInsideRoot(t *testing.T) {
	cfg, _ := importTestDirs(t)
	cfg.DataDir = filepath.Join(cfg.DownloadsDir, "arrmada-data")
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "Dune.2021"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "Dune.2021", "Dune.2021.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Config: cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	_, got, _ := browse(t, srv, cfg.DownloadsDir)
	for _, d := range got.Dirs {
		if d.Name == "arrmada-data" {
			t.Errorf("listed the data folder")
		}
	}
	if code, _, body := browse(t, srv, cfg.DataDir); code != http.StatusBadRequest || !strings.Contains(body, "data folder") {
		t.Errorf("browse the data folder: %d %s", code, body)
	}
	if hits := search(t, srv, "dune"); len(hits) != 0 {
		t.Errorf("search found %+v in the data folder", hits)
	}
	a := &api{deps: Deps{Config: cfg}}
	if s := a.importSuggestions(context.Background(), movieMatcher(movies.Movie{Title: "Dune", Year: 2021}, nil), library.IsVideoFile); len(s) != 0 {
		t.Errorf("suggested from the data folder: %+v", s)
	}
}

func search(t *testing.T, srv *http.Server, q string) []pickerHit {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/manualimport/search?kind=movies&q="+url.QueryEscape(q), "", auth.RoleManager))
	if rec.Code != 200 {
		t.Fatalf("search %q: HTTP %d %s", q, rec.Code, rec.Body.String())
	}
	var out struct {
		Results   []pickerHit `json:"results"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Results
}

func TestImportSearch(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	hits := search(t, srv, "dune 2021")
	if len(hits) != 2 { // the folder and its video
		t.Fatalf("hits = %+v", hits)
	}
	if !hits[0].Dir || hits[0].Where != "Downloads" || hits[0].Rel != "Dune.Part.One.2021.2160p" {
		t.Errorf("first hit = %+v", hits[0])
	}
	if hits := search(t, srv, "notes"); len(hits) != 0 {
		t.Errorf("a non-media file matched: %+v", hits)
	}
	// The library folders aren't searched.
	if err := os.MkdirAll(filepath.Join(cfg.MoviesDir, "Zebra (2001)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, srv, "zebra"); len(hits) != 0 {
		t.Errorf("searched a library: %+v", hits)
	}
	// Too short a query is refused, not a walk of everything.
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/manualimport/search?kind=movies&q=a", "", auth.RoleManager))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("one-letter search: HTTP %d", rec.Code)
	}
}

// The search stops at its depth and result caps.
func TestImportSearchIsBounded(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	for i := 0; i < searchResultsMax+10; i++ {
		if err := os.MkdirAll(filepath.Join(cfg.DownloadsDir, "many", fmt.Sprintf("Match.%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	deep := cfg.DownloadsDir
	for i := 0; i < searchDepth+2; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("d%d", i))
	}
	if err := os.MkdirAll(filepath.Join(deep, "Buried.Treasure"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, srv, "match"); len(hits) != searchResultsMax {
		t.Errorf("%d hits, want the cap %d", len(hits), searchResultsMax)
	}
	if hits := search(t, srv, "buried"); len(hits) != 0 {
		t.Errorf("found something below the depth cap: %+v", hits)
	}
}

func TestImportPickerRoutesNeedManager(t *testing.T) {
	cfg, _, srv := pickerFixture(t)
	for _, target := range []string{
		"/api/v1/manualimport/folders?kind=movies&path=" + url.QueryEscape(cfg.DownloadsDir),
		"/api/v1/manualimport/search?kind=movies&q=dune",
		"/api/v1/movies/1/manualimport/suggestions",
		"/api/v1/series/1/manualimport/suggestions",
		"/api/v1/books/1/manualimport/suggestions",
	} {
		for _, role := range []auth.Role{auth.RoleReadonly, auth.RoleRequester} {
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, lanRequest("GET", target, "", role))
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
				t.Errorf("%s as %s: HTTP %d, want refused", target, role, rec.Code)
			}
		}
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, lanRequest("GET", "/api/v1/manualimport/folders?kind=music", "", auth.RoleManager))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: HTTP %d", rec.Code)
	}
}

func TestImportSuggestionsFromTheDownloadsFolder(t *testing.T) {
	cfg, outside, _ := pickerFixture(t)
	// A matching name reached only through a symlink out of downloads is never offered.
	symlink(t, outside, filepath.Join(cfg.DownloadsDir, "Dune.2021.Linked"))
	a := &api{deps: Deps{Config: cfg}}
	got := a.importSuggestions(context.Background(), movieMatcher(movies.Movie{Title: "Dune", Year: 2021}, nil), library.IsVideoFile)
	if len(got) != 0 {
		// "Dune Part One" isn't "Dune": the title keys must match, not just share a word.
		t.Errorf("suggestions = %+v", got)
	}
	got = a.importSuggestions(context.Background(), movieMatcher(movies.Movie{Title: "Dune: Part One", Year: 2021}, nil), library.IsVideoFile)
	if len(got) != 1 || got[0].Name != "Dune.Part.One.2021.2160p" || got[0].InClient {
		t.Errorf("suggestions = %+v", got)
	}
	got = a.importSuggestions(context.Background(), movieMatcher(movies.Movie{Title: "Loose Film", Year: 2019}, nil), library.IsVideoFile)
	if len(got) != 1 || got[0].SizeBytes != 1 {
		t.Errorf("loose file: %+v", got)
	}
}

func TestSuggestMatchers(t *testing.T) {
	sm := seriesMatcher("Doctor Who", 2005, []string{"Dr Who"})
	for name, want := range map[string]bool{
		"Doctor.Who.2005.S01.1080p":  true,
		"Doctor.Who.S02E03.720p":     true,
		"Dr.Who.S01E01":              true,
		"Doctor.Who.1963.S01.DVDRip": false,
		"Doctor.Foster.S01":          false,
	} {
		if got := sm(name) > 0; got != want {
			t.Errorf("series %q: %v, want %v", name, got, want)
		}
	}
	bm := bookMatcher("Children of Dune", "Frank Herbert")
	if bm("Frank Herbert - Children of Dune (1976) [EPUB]") != 3 || bm("Children.of.Dune.epub") != 2 || bm("Dune Messiah") != 0 {
		t.Errorf("book matcher")
	}
	if bookMatcher("It", "Stephen King")("Kitten Stories") != 0 {
		t.Errorf("a short title matched inside a word")
	}
}

// symlink makes a link or skips the test where the OS won't allow it (Windows without
// developer mode); CI runs on Linux, where it always works.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not permitted here: %v", err)
	}
}
