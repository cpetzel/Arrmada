package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// Through the real router: the other-files list, Add as a version and Stop tracking are
// staff-only, and Stop tracking leaves the version's file on disk.
func TestOtherFilesRoutesAreStaffOnly(t *testing.T) {
	root := t.TempDir()
	s := newRouteServer(t, func(d *Deps) {
		d.Movies = movies.NewService(d.Store.DB(), nil, nil, root, "", nil, d.Log)
	})
	_, kid := s.user(t, "kid@example.com", auth.RoleRequester)
	_, mgr := s.user(t, "mgr@example.com", auth.RoleManager)

	dir := filepath.Join(root, "Heat (1995)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	main, kept := filepath.Join(dir, "Heat (1995).mkv"), filepath.Join(dir, "Heat (1995) Copy.mkv")
	for _, p := range []string{main, kept} {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Truncate(60 << 20)
		_ = f.Close()
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO movies (id, tmdb_id, title, year, has_file, movie_file_path) VALUES (1, 1, 'Heat', 1995, 1, ?)`, []any{main}},
		{`INSERT INTO movie_versions (id, movie_id, label, has_file, file_path) VALUES (5, 1, 'Copy', 1, ?)`, []any{kept}},
	} {
		if _, err := s.st.DB().Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}

	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/movies/1/other-files"},
		{"POST", "/api/v1/movies/1/other-files/version"},
		{"POST", "/api/v1/movies/1/versions/5/untrack"},
	} {
		if rec := s.doBody(r.method, r.path, `{"path":"x"}`, kid); rec.Code != http.StatusForbidden {
			t.Errorf("requester %s %s: HTTP %d, want 403", r.method, r.path, rec.Code)
		}
	}
	if rec := s.do("GET", "/api/v1/movies/1/other-files", mgr); rec.Code != http.StatusOK {
		t.Fatalf("manager list: HTTP %d %s", rec.Code, rec.Body)
	}
	if rec := s.doBody("POST", "/api/v1/movies/1/versions/5/untrack", "", mgr); rec.Code != http.StatusNoContent {
		t.Fatalf("manager untrack: HTTP %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the version's file after Stop tracking: %v", err)
	}
	var n int
	_ = s.st.DB().QueryRow(`SELECT COUNT(*) FROM movie_versions WHERE id = 5`).Scan(&n)
	if n != 0 {
		t.Fatal("the version is still recorded")
	}
}
