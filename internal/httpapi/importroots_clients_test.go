package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/store"
)

// GitHub #70: a second download client on a volume of its own saves outside the
// Downloads folder — here it names the Downloads folder (every grab is sent it) but has
// it mounted elsewhere, which its path mapping says, and it also reports a default save
// folder of its own. Manual import reads both; nothing else widens: a client reporting
// a system folder, the data folder or a whole disk is refused, and so is anything still
// outside every root.
func TestCheckImportPathReadsEachClientsFolders(t *testing.T) {
	cfg, outside := importTestDirs(t)
	base := filepath.Dir(cfg.DownloadsDir)
	second := filepath.Join(base, "second-volume")
	own := filepath.Join(base, "second-default")
	for _, d := range []string{filepath.Join(second, "Film.2020"), own} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	save := own
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/app/preferences":
			fmt.Fprintf(w, `{"save_path":%q}`, save)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dl := download.NewService(st.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	for _, c := range []download.Client{
		{Name: "first", Kind: download.KindQbittorrent, URL: "http://127.0.0.1:1", Enabled: true},
		{Name: "second", Kind: download.KindQbittorrent, URL: srv.URL, Enabled: true,
			PathMaps: download.PathMaps{{Remote: cfg.DownloadsDir, Local: second}}},
	} {
		if _, err := dl.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	a := &api{deps: Deps{Config: cfg, Downloads: dl}}

	for _, p := range []string{second, filepath.Join(second, "Film.2020"), own, cfg.DownloadsDir} {
		if got, err := a.checkImportPath(ctx, p); err != nil || got != filepath.Clean(p) {
			t.Errorf("%q: got %q, %v — want it readable", p, got, err)
		}
	}
	for _, p := range []string{outside, second + "-old", filepath.Join(second, "..")} {
		if _, err := a.checkImportPath(ctx, p); !errors.Is(err, errImportPathOutside) {
			t.Errorf("%q: got %v, want refused", p, err)
		}
	}
	// The data folder stays refused.
	if _, err := a.checkImportPath(ctx, cfg.DataDir); !errors.Is(err, errImportPathDataDir) {
		t.Errorf("data folder: got %v, want refused", err)
	}
}

func TestUsableClientRoot(t *testing.T) {
	cfg, outside := importTestDirs(t)
	file := filepath.Join(outside, "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !usableClientRoot(outside, cfg) {
		t.Errorf("%s refused", outside)
	}
	for _, p := range []string{
		"", "relative/path", "/", "/etc", "/proc/self", "/usr/share", "/var/lib",
		file,                              // not a folder
		filepath.Join(outside, "missing"), // not there
		cfg.DataDir, filepath.Join(cfg.DataDir, "sub"),
		filepath.Dir(cfg.DataDir), // holds the data folder
		cfg.LibraryDir,            // the managed volume
		filepath.Dir(cfg.LibraryDir),
	} {
		if usableClientRoot(p, cfg) {
			t.Errorf("%q accepted", p)
		}
	}
}
