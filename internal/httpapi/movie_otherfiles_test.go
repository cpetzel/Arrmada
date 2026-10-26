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

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/movies"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The film page's other files: GET lists the videos in its folder Arrmada doesn't use,
// and the detail endpoint carries the count.
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

	// Read-only: there is no action on an other file.
	if rec := do("POST", "/api/v1/movies/1/other-files/version", `{"path":"x"}`); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("POST other-files/version: %d, want no such route", rec.Code)
	}
}

// Through the real router: the other-files list is staff-only.
func TestOtherFilesRouteIsStaffOnly(t *testing.T) {
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, root, "", nil, d.Log)
	})
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)
	if _, err := s.st.DB().Exec(`INSERT INTO movies (id, tmdb_id, title, year, has_file, movie_file_path) VALUES (1, 1, 'Heat', 1995, 1, ?)`,
		filepath.Join(root, "Heat (1995)", "Heat (1995).mkv")); err != nil {
		t.Fatal(err)
	}
	if rec := s.do("GET", "/api/v1/movies/1/other-files", kid); rec.Code != http.StatusForbidden {
		t.Errorf("requester: HTTP %d, want 403", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/movies/1/other-files", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: HTTP %d, want 401", rec.Code)
	}
	if rec := s.do("GET", "/api/v1/movies/1/other-files", mgr); rec.Code != http.StatusOK {
		t.Fatalf("manager: HTTP %d %s", rec.Code, rec.Body)
	}
}
