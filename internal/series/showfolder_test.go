package series

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// folderFixture is a series service over one TV folder (/tv unless changed), with shows
// whose episode rows point wherever a test says. Nothing needs to exist on disk: a show's
// folder is read from its first recorded file.
type folderFixture struct {
	t   *testing.T
	db  *sql.DB
	svc *Service
	n   int
}

func newFolderFixture(t *testing.T, root string) *folderFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := NewService(st.DB(), nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return root })
	return &folderFixture{t: t, db: st.DB(), svc: svc}
}

// show adds a series with one episode row per file (season 1, episodes 1..n).
func (f *folderFixture) show(title string, files ...string) int64 {
	f.t.Helper()
	f.n++
	res, err := f.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (?, ?, 1)`, 1000+f.n, title)
	if err != nil {
		f.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	for i, p := range files {
		if _, err := f.db.Exec(`INSERT INTO episodes (series_id, season_number, episode_number, monitored, has_file, file_path)
			VALUES (?, 1, ?, 1, 1, ?)`, id, i+1, filepath.FromSlash(p)); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

// A show's folder is v0.3.1's answer — from its first recorded file, two folders up from a
// "Season N" folder and one up otherwise — whatever the layout: category folders, release
// folders and odd season names included.
func TestShowFolderOfIsV031sAnswer(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ name, file, want string }{
		{"Season N", "/tv/The Bear (2022)/Season 01/The Bear - S01E01.mkv", "/tv/The Bear (2022)"},
		{"Season.01", "/tv/Andor/Season.01/Andor - S01E01.mkv", "/tv/Andor"},
		{"Specials", "/tv/The Bear (2022)/Specials/The Bear - S00E01.mkv", "/tv/The Bear (2022)"},
		{"flat", "/tv/Bluey/Bluey - S01E01.mkv", "/tv/Bluey"},
		{"category", "/tv/Anime/Frieren/Season 1/Frieren - S01E01.mkv", "/tv/Anime/Frieren"},
		{"release folder", "/tv/Show/Season 1/Show.S01E01.1080p/Show.S01E01.mkv", "/tv/Show/Season 1/Show.S01E01.1080p"},
		{"odd season name", "/tv/Avatar/Book 1 Water/Avatar - S01E01.mkv", "/tv/Avatar/Book 1 Water"},
		{"show named 24", "/tv/24/Season 1/24 - S01E01.mkv", "/tv/24"},
		{"show named 1992, flat", "/tv/1992/1992 - S01E01.mkv", "/tv/1992"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFolderFixture(t, "/tv")
			id := f.show("Show", c.file)
			info := f.svc.ShowFolderOf(ctx, id)
			if info.Unknown || info.Shape56 || info.Path != filepath.FromSlash(c.want) {
				t.Fatalf("ShowFolderOf = %+v, want %s", info, c.want)
			}
			if got := f.svc.ExistingFolderName(ctx, id); got != filepath.Base(c.want) {
				t.Errorf("ExistingFolderName = %q", got)
			}
			if got := f.svc.FolderPath(ctx, id); got != filepath.FromSlash(c.want) {
				t.Errorf("FolderPath = %q", got)
			}
		})
	}
}

// When v0.3.1's answer is the #56 mistake — a folder named like a season (S01, Stagione 1,
// Series 1, a season template's name), or the TV folder itself — the show's folder is
// unknown: no folder is guessed, above the files least of all. FileDir is the first file's
// own folder; FolderPath (for the media server) is that folder only when v0.3.1 read it
// too, and empty otherwise — never a folder above the files.
func TestShowFolderOf56Shapes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, file, fileDir string
		folderPath          bool // FolderPath is FileDir: v0.3.1 read that folder too
	}{
		{"S12", "/tv/Pechino Express/S12/Pechino Express - S12E01.mkv", "/tv/Pechino Express/S12", true},
		{"Stagione", "/tv/Gomorra/Stagione 1/Gomorra - S01E01.mkv", "/tv/Gomorra/Stagione 1", true},
		{"Series", "/tv/Taskmaster/Series 1/Taskmaster - S01E01.mkv", "/tv/Taskmaster/Series 1", true},
		{"in a category", "/tv/Kids/S01/Show - S01E01.mkv", "/tv/Kids/S01", true},
		{"a #56 leftover", "/tv/S13/Season 12/Show - S12E01.mkv", "/tv/S13/Season 12", false},
		{"a show named like a season", "/tv/Season 1/Season 01/Season 1 - S01E01.mkv", "/tv/Season 1/Season 01", false},
		{"loose in the TV folder", "/tv/Show - S01E01.mkv", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFolderFixture(t, "/tv")
			id := f.show("Show", c.file)
			info := f.svc.ShowFolderOf(ctx, id)
			if !info.Unknown || !info.Shape56 || info.Path != "" {
				t.Fatalf("ShowFolderOf = %+v, want #56-shaped", info)
			}
			if !strings.Contains(info.Why, "Season 01") {
				t.Errorf("why = %q, want it to say how to rename the folders", info.Why)
			}
			want := ""
			if c.fileDir != "" {
				want = filepath.FromSlash(c.fileDir)
			}
			if info.FileDir != want {
				t.Errorf("FileDir = %q, want %q", info.FileDir, want)
			}
			wantFP := ""
			if c.folderPath {
				wantFP = want
			}
			if got := f.svc.FolderPath(ctx, id); got != wantFP {
				t.Errorf("FolderPath = %q, want %q", got, wantFP)
			}
			if got := f.svc.ExistingFolderName(ctx, id); got != "" {
				t.Errorf("ExistingFolderName = %q", got)
			}
			if f.svc.FolderUnclear(ctx, id) == "" {
				t.Error("FolderUnclear is empty")
			}
		})
	}
	// A season folder template's names count too.
	f := newFolderFixture(t, "/tv")
	f.svc.SetSeasonFolderMatcher(func(name string) bool { return strings.HasPrefix(name, "The Pitt Season ") })
	if info := f.svc.ShowFolderOf(ctx, f.show("The Pitt", "/tv/The Pitt/The Pitt Season 01/The Pitt - S01E01.mkv")); !info.Shape56 {
		t.Errorf("template season folder: %+v", info)
	}
	// A show with no files has no folder yet, and isn't refused.
	if info := f.svc.ShowFolderOf(ctx, f.show("New")); info.Unknown || info.Path != "" {
		t.Errorf("new show: %+v", info)
	}
}

// ShowFolderFor, for Plex scans and deletes, is v0.3.1's folder, or the file's own folder
// when that's the #56 mistake: never a folder above the files.
func TestShowFolderForNeverClimbsAboveA56Folder(t *testing.T) {
	f := newFolderFixture(t, "/tv")
	for file, want := range map[string]string{
		"/tv/Show/Season 1/x.mkv":             "/tv/Show",
		"/tv/Pechino Express/S12/x.mkv":       "/tv/Pechino Express/S12",
		"/tv/S13/Season 12/x.mkv":             "/tv/S13/Season 12",
		"/tv/Anime/Frieren/Season 1/x.mkv":    "/tv/Anime/Frieren",
		"/tv/x.mkv":                           "",
		"/elsewhere/Gomorra/Stagione 1/x.mkv": "/elsewhere/Gomorra/Stagione 1",
	} {
		if got := f.svc.ShowFolderFor(filepath.FromSlash(file)); got != filepath.FromSlash(want) && !(want == "" && got == "") {
			t.Errorf("ShowFolderFor(%s) = %q, want %q", file, got, want)
		}
	}
}

// The last check before a file is placed refuses a folder named like a season, a TV
// folder (or one named after the TV folder it's in), and a folder another show's files
// are in.
func TestUnsafeImportFolder(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	a := f.show("Lost", "/tv/S01/Season 1/Lost - S01E01.mkv")
	b := f.show("Fringe", "/tv/Fringe/Season 1/Fringe - S01E01.mkv")
	for dir, wantRefused := range map[string]bool{
		"/tv/S13":        true,
		"/tv/Stagione 2": true,
		"/tv":            true,
		"/tv/tv":         true,
		"/tv/S01":        true,
		"/tv/Fringe":     false,
		"/tv/New Show":   false,
		"/tv/1992":       false,
	} {
		why := f.svc.UnsafeImportFolder(ctx, Series{ID: b, Title: "Fringe"}, filepath.FromSlash(dir))
		if (why != "") != wantRefused {
			t.Errorf("UnsafeImportFolder(%s) = %q, want refused=%v", dir, why, wantRefused)
		}
	}
	if why := f.svc.UnsafeImportFolder(ctx, Series{ID: a, Title: "Lost"}, filepath.FromSlash("/tv/Fringe")); why == "" {
		t.Error("Lost may import into Fringe's folder")
	}
	err := &ShowFolderError{Title: "Lost", Why: "x"}
	if !errors.Is(err, ErrNoShowFolder) || !strings.Contains(err.Error(), "Can't tell which folder Lost lives in") {
		t.Errorf("ShowFolderError = %v", err)
	}
}

// The library scan doesn't take a folder named like a season, right in the TV folder, for
// a show: those hold files misplaced before #56 was fixed.
func TestScanSkipsSeasonNamedFolders(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tv := t.TempDir()
	meta := shelfMeta{shows: map[int]metadata.SeriesDetails{
		1: {SeriesResult: metadata.SeriesResult{TMDBID: 1, Title: "Bluey", Year: 2020}, Seasons: listing(2)},
		2: {SeriesResult: metadata.SeriesResult{TMDBID: 2, Title: "Stagione", Year: 2020}, Seasons: listing(2)},
	}}
	svc := NewService(st.DB(), meta, tv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	touchEpisode(t, filepath.Join(tv, "Bluey (2020)", "S01", "Bluey - S01E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "Stagione 2", "Season 2", "Bluey - S02E01.mkv"))
	touchEpisode(t, filepath.Join(tv, "S13", "Season 12", "Bluey - S12E01.mkv"))
	res, err := svc.ScanLibrary(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || len(res.Unmatched) != 0 {
		t.Fatalf("scan = %+v, want only Bluey", res)
	}
	if _, err := svc.repo.GetByTMDB(context.Background(), 2); err == nil {
		t.Error(`the "Stagione 2" folder was added as the show "Stagione"`)
	}
}

// SeasonDir: for a #56-shaped show, the one folder a season's files are in, and only when
// it holds nothing but the show's files.
func TestSeasonDir(t *testing.T) {
	ctx := context.Background()
	tv := t.TempDir()
	f := newFolderFixture(t, tv)
	mk := func(rel string) string {
		p := filepath.Join(tv, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(p)
	}
	pitt := f.show("The Pitt", mk("The Pitt/S01/The Pitt - S01E01.mkv"), mk("The Pitt/S01/The Pitt - S01E02.mkv"))
	if dir, why := f.svc.SeasonDir(ctx, pitt, 1); why != "" || dir != filepath.Join(tv, "The Pitt", "S01") {
		t.Errorf("SeasonDir(1) = %q, %q", dir, why)
	}
	if _, why := f.svc.SeasonDir(ctx, pitt, 2); why == "" {
		t.Error("a new season got a folder")
	}
	mk("The Pitt/S01/someone else.mkv")
	if _, why := f.svc.SeasonDir(ctx, pitt, 1); why == "" {
		t.Error("a season folder holding another video was accepted")
	}
	kids := f.show("Show", mk("Kids/S01/Show - S01E01.mkv"))
	mk("Kids/S01/.hidden/Bluey - S01E01.mkv")
	if _, why := f.svc.SeasonDir(ctx, kids, 1); why == "" {
		t.Error("a season folder with a hidden folder of videos was accepted")
	}
	split := f.show("Split", mk("Split/S01/a - S01E01.mkv"), mk("Other/S01/a - S01E02.mkv"))
	if _, why := f.svc.SeasonDir(ctx, split, 1); why == "" {
		t.Error("a season in two folders got one")
	}
	left := f.show("Leftover", mk("S13/Season 12/Leftover - S01E01.mkv"))
	if _, why := f.svc.SeasonDir(ctx, left, 1); why == "" {
		t.Error("a #56 leftover folder was used")
	}
}
