package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The film page's other files: GET lists the videos in its folder Arrmada doesn't use,
// POST records one where it is as a new version, and the detail endpoint carries the count.
func TestMovieOtherFilesEndpoints(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	root := t.TempDir()
	mv := movies.NewService(st.DB(), nil, nil, root, "", nil, log)
	a := &api{deps: Deps{Store: st, Log: log, Movies: mv}}

	dir := filepath.Join(root, "Heat (1995)")
	sparse := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(60 << 20); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		return p
	}
	main := sparse("Heat (1995) Bluray-1080p.mkv")
	extra := sparse("Heat (1995) Copy 2160p.mkv")
	sparse("Heat (1995)-trailer.mkv")
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO movies (id, tmdb_id, title, year, has_file, movie_file_path) VALUES (1, 1, 'Heat', 1995, 1, ?)`, main); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/movies/{id}/other-files", a.handleMovieOtherFiles)
	mux.HandleFunc("POST /api/v1/movies/{id}/other-files/version", a.handleAddOtherFileAsVersion)
	mux.HandleFunc("GET /api/v1/movies/{id}", a.handleGetMovie)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	rec := do("GET", "/api/v1/movies/1/other-files", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list movies.OtherFiles
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || len(list.Files) != 1 || list.Files[0].Path != extra || list.Files[0].RelPath != "Heat (1995) Copy 2160p.mkv" {
		t.Fatalf("list = %+v, want the 2160p copy only", list)
	}

	rec = do("GET", "/api/v1/movies/1", "")
	var detail struct {
		OtherFiles *movies.OtherFiles `json:"other_files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil || detail.OtherFiles == nil || detail.OtherFiles.Count != 1 {
		t.Fatalf("detail other_files = %+v (%v), want a count of 1", detail.OtherFiles, err)
	}

	body, _ := json.Marshal(map[string]any{"path": filepath.Join(dir, "Heat (1995)-trailer.mkv")})
	if rec := do("POST", "/api/v1/movies/1/other-files/version", string(body)); rec.Code != http.StatusConflict {
		t.Fatalf("a trailer as a version: %d %s, want 409", rec.Code, rec.Body)
	}
	body, _ = json.Marshal(map[string]any{"path": extra, "label": "4K copy"})
	rec = do("POST", "/api/v1/movies/1/other-files/version", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("add as a version: %d %s", rec.Code, rec.Body)
	}
	var v movies.Version
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Label != "4K copy" || v.FilePath != extra || v.Monitored {
		t.Fatalf("version = %+v (%v), want '4K copy' holding the file where it is, unmonitored", v, err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("the file must stay where it was: %v", err)
	}
	rec = do("GET", "/api/v1/movies/1/other-files", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || list.Count != 0 {
		t.Fatalf("after: %+v (%v), want none", list, err)
	}
}
