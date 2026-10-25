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
	"runtime"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/store"
)

// qbitReporting is a qBittorrent that says it saves into save.
func qbitReporting(t *testing.T, save string) string {
	t.Helper()
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
	return srv.URL
}

// importAPI is an api whose download clients are the given ones.
func importAPI(t *testing.T, cfg config.Config, clients ...download.Client) *api {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dl := download.NewService(st.DB(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, c := range clients {
		if _, err := dl.Create(context.Background(), c); err != nil {
			t.Fatal(err)
		}
	}
	return &api{deps: Deps{Config: cfg, Downloads: dl}}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// GitHub #70: a second download client on a volume of its own. It names the Downloads
// folder (every grab is sent it) but an admin mapped where Arrmada sees it, so its folder
// is readable by manual import. A folder it merely reports (its default save path) is not
// — the refusal says how an admin can allow it — and neither is anything else outside.
func TestCheckImportPathReadsAClientsMappedFolder(t *testing.T) {
	cfg, outside := importTestDirs(t)
	base := filepath.Dir(cfg.DownloadsDir)
	second := filepath.Join(base, "second-volume")
	own := filepath.Join(base, "second-default")
	mkdirs(t, filepath.Join(second, "Film.2020"), filepath.Join(own, "Show.S01"))
	a := importAPI(t, cfg,
		download.Client{Name: "first", Kind: download.KindQbittorrent, URL: "http://127.0.0.1:1", Enabled: true},
		download.Client{Name: "second", Kind: download.KindQbittorrent, URL: qbitReporting(t, own), Enabled: true,
			PathMaps: download.PathMaps{{Remote: cfg.DownloadsDir, Local: second}}},
		// Switched off: its mapping isn't a root.
		download.Client{Name: "off", Kind: download.KindQbittorrent, URL: "http://127.0.0.1:1", Enabled: false,
			PathMaps: download.PathMaps{{Remote: "/x", Local: outside}}},
	)
	ctx := context.Background()

	for _, p := range []string{second, filepath.Join(second, "Film.2020"), cfg.DownloadsDir} {
		if got, err := a.checkImportPath(ctx, p); err != nil || got != filepath.Clean(p) {
			t.Errorf("%q: got %q, %v — want it readable", p, got, err)
		}
	}
	for _, p := range []string{outside, second + "-old", filepath.Join(second, "..")} {
		if _, err := a.checkImportPath(ctx, p); !errors.Is(err, errImportPathOutside) {
			t.Errorf("%q: got %v, want refused", p, err)
		}
	}
	// The client's own default folder: refused, with the way to allow it.
	_, err := a.checkImportPath(ctx, filepath.Join(own, "Show.S01"))
	if !errors.Is(err, errImportPathOutside) || !strings.Contains(err.Error(), "add a path mapping to second") ||
		!strings.Contains(err.Error(), own+" in Arrmada") {
		t.Errorf("reported folder: got %v, want refused with how to allow it", err)
	}
	if _, err := a.checkImportPath(ctx, cfg.DataDir); !errors.Is(err, errImportPathDataDir) {
		t.Errorf("data folder: got %v, want refused", err)
	}
	// The file-details check reads the same roots.
	if !a.underLibraryRoot(ctx, filepath.Join(second, "Film.2020")) || a.underLibraryRoot(ctx, own) {
		t.Error("file details don't follow manual import's roots")
	}
}

// Whatever a client reports — a share, the container's temp or system folders, a link to
// /etc, a folder inside the managed library volume — manual import still refuses it, end
// to end. So does a mapping an admin pointed at a system folder (through a link) or into
// the managed library volume.
func TestCheckImportPathRefusesHostileClientFolders(t *testing.T) {
	cfg, outside := importTestDirs(t)
	base := filepath.Dir(cfg.DownloadsDir)
	share := filepath.Join(base, "storage")
	inLib := filepath.Join(cfg.LibraryDir, "dl")
	mkdirs(t, filepath.Join(share, "x"), filepath.Join(inLib, "x"))
	etcLink := filepath.Join(base, "etc-link")
	linked := runtime.GOOS != "windows" && os.Symlink("/etc", etcLink) == nil
	ctx := context.Background()

	type tc struct{ reported, path string }
	cases := []tc{
		{share, filepath.Join(share, "x")},
		{outside, outside},
		{inLib, filepath.Join(inLib, "x")},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, tc{"/tmp", os.TempDir()}, tc{"/etc", "/etc"}, tc{"/etcetera", "/etcetera"})
		if linked {
			cases = append(cases, tc{etcLink, filepath.Join(etcLink, "hostname")})
		}
	}
	for _, c := range cases {
		a := importAPI(t, cfg, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: qbitReporting(t, c.reported), Enabled: true},
			download.Client{Name: "qb2", Kind: download.KindQbittorrent, URL: qbitReporting(t, c.reported), Enabled: true})
		if _, err := a.checkImportPath(ctx, c.path); !errors.Is(err, errImportPathOutside) {
			t.Errorf("reported %q: %q got %v, want refused", c.reported, c.path, err)
		}
	}

	mapped := []tc{{inLib, filepath.Join(inLib, "x")}}
	if linked {
		mapped = append(mapped, tc{etcLink, filepath.Join(etcLink, "hostname")})
	}
	for _, c := range mapped {
		a := importAPI(t, cfg, download.Client{Name: "qb", Kind: download.KindQbittorrent, URL: "http://127.0.0.1:1", Enabled: true,
			PathMaps: download.PathMaps{{Remote: "/data/dl", Local: c.reported}}})
		if _, err := a.checkImportPath(ctx, c.path); !errors.Is(err, errImportPathOutside) {
			t.Errorf("mapped %q: %q got %v, want refused", c.reported, c.path, err)
		}
	}
}

func TestUsableClientRoot(t *testing.T) {
	cfg, outside := importTestDirs(t)
	file := filepath.Join(outside, "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	inLib := filepath.Join(cfg.LibraryDir, "dl")
	mkdirs(t, inLib)
	if !usableClientRoot(outside, cfg) {
		t.Errorf("%s refused", outside)
	}
	refused := []string{
		"", "relative/path",
		file,                              // not a folder
		filepath.Join(outside, "missing"), // not there
		cfg.DataDir, filepath.Join(cfg.DataDir, "sub"),
		filepath.Dir(cfg.DataDir), // holds the data folder
		cfg.LibraryDir,            // the managed volume
		inLib,                     // inside it
		filepath.Dir(cfg.LibraryDir),
	}
	if runtime.GOOS != "windows" {
		refused = append(refused, "/", "/etc", "/proc/self", "/usr/share", "/var/lib")
	}
	for _, p := range refused {
		if usableClientRoot(p, cfg) {
			t.Errorf("%q accepted", p)
		}
	}
}
