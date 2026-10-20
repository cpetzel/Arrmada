package series

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libfolders"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// folderFixture is a series service over one TV folder (/tv unless changed), with shows
// whose episode rows point wherever a test says. Nothing needs to exist on disk: which
// folder a show lives in is read from its recorded files (and every recorded file counts
// as there, unless a test says otherwise), and folders are listed from the files the test
// recorded or put on its pretend disk (onDisk).
type folderFixture struct {
	t     *testing.T
	db    *sql.DB
	svc   *Service
	n     int
	files map[string]bool // the pretend disk
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
	svc.fileGone = func(string) bool { return false }
	f := &folderFixture{t: t, db: st.DB(), svc: svc, files: map[string]bool{}}
	svc.readDir = f.readDir
	return f
}

// onDisk puts files on the pretend disk without recording them for any show.
func (f *folderFixture) onDisk(paths ...string) {
	for _, p := range paths {
		f.files[filepath.Clean(filepath.FromSlash(p))] = true
	}
}

// readDir lists a folder of the pretend disk.
func (f *folderFixture) readDir(dir string) ([]fs.DirEntry, error) {
	dir = filepath.Clean(dir)
	seen := map[string]bool{}
	var out []fs.DirEntry
	for p := range f.files {
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		out = append(out, fakeEntry{name: parts[0], dir: len(parts) > 1})
	}
	if len(out) == 0 {
		return nil, fs.ErrNotExist
	}
	return out, nil
}

type fakeEntry struct {
	name string
	dir  bool
}

func (e fakeEntry) Name() string { return e.name }
func (e fakeEntry) IsDir() bool  { return e.dir }
func (e fakeEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

// show adds a series with one episode row per file (season 1, episodes 1..n), the files on
// the pretend disk.
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
	f.onDisk(files...)
	return id
}

// Season folders under every name people use — and none at all — give the show's folder,
// not the season folder. A show named like a number ("1992", "24") stays itself.
func TestShowFolderOfSeasonFolderNames(t *testing.T) {
	for _, c := range []struct{ name, file, want string }{
		{"S01", "/tv/Pechino Express/S12/Pechino Express - S12E01.mkv", "/tv/Pechino Express"},
		{"Stagione", "/tv/Gomorra/Stagione 1/Gomorra - S01E01.mkv", "/tv/Gomorra"},
		{"Saison", "/tv/Lupin/Saison 1/Lupin - S01E01.mkv", "/tv/Lupin"},
		{"Staffel", "/tv/Dark/Staffel 01/Dark - S01E01.mkv", "/tv/Dark"},
		{"Season.01", "/tv/Show/Season.01/Show - S01E01.mkv", "/tv/Show"},
		{"Series", "/tv/Taskmaster/Series 1/Taskmaster - S01E01.mkv", "/tv/Taskmaster"},
		{"Temporada", "/tv/Elite/Temporada 2/Elite - S02E01.mkv", "/tv/Elite"},
		{"flat", "/tv/The Bear (2022)/The Bear - S01E01.mkv", "/tv/The Bear (2022)"},
		// Not #56: v0.3.1's own answer stands, right or wrong.
		{"release folder in season", "/tv/Show/Season 1/Show.S01E01.1080p/Show.S01E01.mkv", "/tv/Show/Season 1/Show.S01E01.1080p"},
		{"odd season name", "/tv/Avatar/Book 1 Water/Avatar - S01E01.mkv", "/tv/Avatar/Book 1 Water"},
		{"show named 1992", "/tv/1992/S01/1992 - S01E01.mkv", "/tv/1992"},
		{"show named 24", "/tv/24/Season 1/24 - S01E01.mkv", "/tv/24"},
		{"S.W.A.T.", "/tv/S.W.A.T. (2017)/S01/S.W.A.T. - S01E01.mkv", "/tv/S.W.A.T. (2017)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFolderFixture(t, "/tv")
			id := f.show("Show", c.file)
			info := f.svc.ShowFolderOf(context.Background(), id)
			if info.Unknown || info.Path != filepath.FromSlash(c.want) {
				t.Fatalf("ShowFolderOf = %+v, want %s", info, c.want)
			}
			if got := f.svc.ExistingFolderName(context.Background(), id); got != filepath.Base(c.want) {
				t.Errorf("ExistingFolderName = %q, want %q", got, filepath.Base(c.want))
			}
		})
	}
}

// Outside every TV folder there's no TV folder to check a corrected folder against, so a
// show's folder is v0.3.1's and an import for a season-named one is refused.
func TestShowFolderOutsideTheTVFolder(t *testing.T) {
	for file, want := range map[string][2]string{
		// file: {ShowFolder's name-only guess, ShowFolderFor (v0.3.1's)}
		"/old/Gomorra/Stagione 1/x.mkv": {"/old/Gomorra", "/old/Gomorra/Stagione 1"},
		"/old/Show/S01/x.mkv":           {"/old/Show", "/old/Show/S01"},
		"/old/Show/Specials/x.mkv":      {"/old/Show", "/old/Show"},
		"/old/1992/x.mkv":               {"/old/1992", "/old/1992"},
		"/old/1883/Season 1/x.mkv":      {"/old/1883", "/old/1883"},
	} {
		if got := ShowFolder(filepath.FromSlash(file)); got != filepath.FromSlash(want[0]) {
			t.Errorf("ShowFolder(%s) = %s, want %s", file, got, want[0])
		}
		f := newFolderFixture(t, "/tv")
		if got := f.svc.ShowFolderFor(filepath.FromSlash(file)); got != filepath.FromSlash(want[1]) {
			t.Errorf("ShowFolderFor(%s) = %s, want %s", file, got, want[1])
		}
	}
	f := newFolderFixture(t, "/tv")
	if info := f.svc.ShowFolderOf(context.Background(), f.show("Gomorra", "/old/Gomorra/Stagione 1/x.mkv")); !info.Unknown {
		t.Errorf("season-named folder outside the TV folder: %+v, want refused", info)
	}
}

// The show's folder is where most of its files are. Files the season-folder bug put in
// <TV>/S13/Season 12 are outvoted — and even when they're the majority, never chosen,
// because a folder named like a season is never a show's folder (#56).
func TestShowFolderOfIgnoresMisplacedFiles(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	id := f.show("Pechino Express",
		"/tv/Pechino Express/S12/Pechino Express - S12E01.mkv",
		"/tv/Pechino Express/S12/Pechino Express - S12E02.mkv",
		"/tv/S13/Season 12/Pechino Express - S12E03.mkv",
		"/tv/S13/Season 12/Pechino Express - S12E04.mkv",
		"/tv/S13/Season 13/Pechino Express - S13E01.mkv",
	)
	info := f.svc.ShowFolderOf(ctx, id)
	if info.Unknown || info.Path != filepath.FromSlash("/tv/Pechino Express") || info.Files != 2 || info.Total != 5 {
		t.Fatalf("ShowFolderOf = %+v, want /tv/Pechino Express holding 2 of 5", info)
	}

	// Every file misplaced: there's no telling where the show lives.
	lost := f.show("Godfather of Harlem", "/tv/S01/Season 4/Godfather of Harlem - S04E01.mkv")
	info = f.svc.ShowFolderOf(ctx, lost)
	if !info.Unknown || info.Path != "" || !strings.Contains(info.Why, "named like a season folder") {
		t.Fatalf("all misplaced: ShowFolderOf = %+v, want Unknown", info)
	}
	if got := f.svc.ExistingFolderName(ctx, lost); got != "" {
		t.Errorf("ExistingFolderName = %q, want none", got)
	}
	// Once the misplaced files have been moved away by hand they no longer count against
	// the show; files in its own folder are never checked on disk. A show whose every file
	// was such a leftover isn't a new show, though: refused until a rescan finds them.
	f.svc.fileGone = func(p string) bool { return strings.Contains(p, filepath.FromSlash("/tv/S")) }
	if info := f.svc.ShowFolderOf(ctx, lost); !info.Unknown || info.Path != "" {
		t.Errorf("misplaced files gone: ShowFolderOf = %+v", info)
	}
	if info := f.svc.ShowFolderOf(ctx, id); info.Path != filepath.FromSlash("/tv/Pechino Express") || info.Total != 2 {
		t.Errorf("Pechino Express with its misplaced files gone = %+v", info)
	}
}

// Two shows' files in one folder: it's neither's for sure, so neither imports into it.
func TestShowFolderOfSharedFolder(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	a := f.show("Teen Titans", "/tv/Teen Titans/Season 1/Teen Titans - S01E01.mkv")
	b := f.show("Teen Titans Go!", "/tv/Teen Titans/Season 1/Teen Titans Go! - S01E01.mkv")
	for _, id := range []int64{a, b} {
		if info := f.svc.ShowFolderOf(ctx, id); !info.Unknown || !strings.Contains(info.Why, "also holds episodes of") {
			t.Errorf("series %d: ShowFolderOf = %+v, want Unknown (shared)", id, info)
		}
	}
	// The prefix is a whole folder: "Teen Titans Go" isn't inside "Teen Titans".
	c := f.show("Teen Titans Go", "/tv/Teen Titans Go/Season 1/Teen Titans Go - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, c); info.Unknown || info.Path != filepath.FromSlash("/tv/Teen Titans Go") {
		t.Errorf("Teen Titans Go = %+v", info)
	}
}

// Below a folder inside the TV folder (a category, or a TV folder set one level too high)
// a show is read as v0.3.1 read it — and a season-named folder there, which v0.3.1 got
// wrong, is refused rather than corrected: the folder above it isn't right inside the TV
// folder, so nothing proves it's the show's.
func TestShowFolderBelowAFolderInsideTheTVFolder(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/storage")
	pe := f.show("Pechino Express", "/storage/TV Shows/Pechino Express/S12/Pechino Express - S12E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, pe); !info.Unknown {
		t.Errorf("Pechino Express = %+v, want refused", info)
	}
	fargo := f.show("Fargo", "/storage/TV Shows/Fargo/Season 1/Fargo - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, fargo); info.Unknown || info.Path != filepath.FromSlash("/storage/TV Shows/Fargo") {
		t.Errorf("Fargo = %+v, want v0.3.1's /storage/TV Shows/Fargo", info)
	}
	// v0.3.1 imported Fargo into <TV folder>/Fargo, and so does this.
	if got := f.svc.ImportDir(ctx, Series{ID: fargo}); got != "" {
		t.Errorf("ImportDir = %q, want the TV folder", got)
	}
	if got := f.svc.ExistingFolderName(ctx, fargo); got != "Fargo" {
		t.Errorf("ExistingFolderName = %q", got)
	}
}

// The folder above a season-named folder is the show's only when it holds this show and
// nothing else on disk: another show's folder (tracked or not), a season folder without
// any of the show's files, or loose videos that aren't the show's mean it isn't.
func TestShowFolderCorrectionNeedsTheFolderToHoldOnlyTheShow(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		files  []string
		disk   []string
		wantOK bool
	}{
		{"just the show", []string{"/tv/Pechino Express/S12/a - S12E01.mkv", "/tv/Pechino Express/S13/a - S13E01.mkv"}, nil, true},
		{"with its extras", []string{"/tv/Pechino Express/S12/a - S12E01.mkv"}, []string{"/tv/Pechino Express/Extras/making of.mkv"}, true},
		{"category with an untracked show", []string{"/tv/Kids/S01/a - S01E01.mkv"}, []string{"/tv/Kids/Bluey/Season 1/Bluey - S01E01.mkv"}, false},
		{"franchise with an untracked show", []string{"/tv/Star Trek/S01/a - S01E01.mkv"},
			[]string{"/tv/Star Trek/Star Trek The Next Generation/Season 1/TNG - S01E01.mkv"}, false},
		{"category with an untracked flat show", []string{"/tv/Kids/S01/a - S01E01.mkv"}, []string{"/tv/Kids/Bluey/Bluey - S01E01.mkv"}, false},
		{"a season folder none of its files are in", []string{"/tv/Pechino Express/S12/a - S12E01.mkv"}, []string{"/tv/Pechino Express/S11/a - S11E01.mkv"}, false},
		{"a loose video that isn't its", []string{"/tv/Pechino Express/S12/a - S12E01.mkv"}, []string{"/tv/Pechino Express/other.mkv"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFolderFixture(t, "/tv")
			f.onDisk(c.disk...)
			id := f.show("Show", c.files...)
			info := f.svc.ShowFolderOf(ctx, id)
			if info.Unknown == c.wantOK {
				t.Errorf("ShowFolderOf = %+v, want ok=%v", info, c.wantOK)
			}
		})
	}
}

// A show's files that name different folders: v0.3.1 took whichever came first; nothing
// is guessed now.
func TestShowFolderOfFilesThatDisagree(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	avatar := f.show("Avatar", "/tv/Cartoons/Avatar/Book 1 Water/a - S01E01.mkv", "/tv/Cartoons/Avatar/Book 2 Earth/a - S02E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, avatar); !info.Unknown || !strings.Contains(info.Why, "different folders") {
		t.Errorf("Avatar = %+v, want refused", info)
	}
	// Two folders v0.3.1 imported into the same place agree: <TV>/Frieren either way.
	split := f.show("Frieren", "/tv/Anime/Frieren/Season 1/f - S01E01.mkv", "/tv/Frieren/Season 1/f - S01E02.mkv", "/tv/Anime/Frieren/Season 1/f - S01E03.mkv")
	if info := f.svc.ShowFolderOf(ctx, split); info.Unknown || info.Path != filepath.FromSlash("/tv/Anime/Frieren") || f.svc.ExistingFolderName(ctx, split) != "Frieren" {
		t.Errorf("Frieren = %+v", info)
	}
}

// A show whose only files sit loose in the TV folder: v0.3.1 imported into a folder named
// after the TV folder. Refused.
func TestShowFolderOfLooseFiles(t *testing.T) {
	f := newFolderFixture(t, "/tv")
	id := f.show("Show", "/tv/Show - S01E01.mkv")
	if info := f.svc.ShowFolderOf(context.Background(), id); !info.Unknown || info.Path != "" {
		t.Fatalf("ShowFolderOf = %+v, want refused", info)
	}
	if got := f.svc.ShowFolderFor(filepath.FromSlash("/tv/Show - S01E01.mkv")); got != "" {
		t.Errorf("ShowFolderFor(loose) = %q, want none", got)
	}
}

// Several TV folders: the show's folder is the first folder below whichever TV folder its
// files are in, and its episodes import under that TV folder.
func TestShowFolderOfSeveralTVFolders(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	base := t.TempDir()
	tv, animeDir := filepath.Join(base, "tv"), filepath.Join(base, "anime")
	for _, d := range []string{tv, animeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := libroots.New(nil, config.Config{TVDir: tv})
	folders := libfolders.New(st.DB(), roots, filepath.Join(base, "data"))
	if _, err := folders.Add(ctx, libfolders.AddInput{Media: libfolders.MediaSeries, Path: animeDir}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(st.DB(), nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.SetRootFunc(func() string { return tv })
	svc.SetFolders(folders)
	f := &folderFixture{t: t, db: st.DB(), svc: svc, files: map[string]bool{}}
	svc.readDir = f.readDir
	id := f.show("Frieren", filepath.ToSlash(filepath.Join(animeDir, "Frieren", "S01", "Frieren - S01E01.mkv")))
	info := svc.ShowFolderOf(ctx, id)
	if info.Unknown || info.Path != filepath.Join(animeDir, "Frieren") {
		t.Fatalf("ShowFolderOf = %+v", info)
	}
	if got := svc.ImportDir(ctx, Series{ID: id}); got != animeDir {
		t.Errorf("ImportDir = %q, want the Anime folder", got)
	}
	// A misplaced copy in the other TV folder's "S01" doesn't move it.
	if _, err := st.DB().Exec(`INSERT INTO episodes (series_id, season_number, episode_number, monitored, has_file, file_path)
		VALUES (?, 1, 9, 1, 1, ?)`, id, filepath.Join(tv, "S01", "Season 1", "Frieren - S01E09.mkv")); err != nil {
		t.Fatal(err)
	}
	if info := svc.ShowFolderOf(ctx, id); info.Path != filepath.Join(animeDir, "Frieren") {
		t.Errorf("with a misplaced copy: ShowFolderOf = %+v", info)
	}
}

// The last check before a file is placed refuses a folder named like a season, a TV
// folder, and a folder another show's files are in.
func TestUnsafeImportFolder(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	a := f.show("Lost", "/tv/S01/Season 1/Lost - S01E01.mkv")
	b := f.show("Fringe", "/tv/Fringe/S01/Fringe - S01E01.mkv")
	for dir, wantRefused := range map[string]bool{
		"/tv/S13":        true,  // named like a season
		"/tv/Stagione 2": true,  // likewise
		"/tv":            true,  // the TV folder itself
		"/tv/tv":         true,  // named after it (the old flat-layout bug)
		"/tv/S01":        true,  // Lost's misplaced files are there
		"/tv/Fringe":     false, // Fringe's own folder, for Fringe
		"/tv/New Show":   false,
		"/tv/1992":       false,
	} {
		why := f.svc.UnsafeImportFolder(ctx, Series{ID: b, Title: "Fringe"}, filepath.FromSlash(dir))
		if (why != "") != wantRefused {
			t.Errorf("UnsafeImportFolder(%s) = %q, want refused=%v", dir, why, wantRefused)
		}
	}
	// Fringe's folder is another show's, for Lost.
	if why := f.svc.UnsafeImportFolder(ctx, Series{ID: a, Title: "Lost"}, filepath.FromSlash("/tv/Fringe")); why == "" {
		t.Error("Lost may import into Fringe's folder")
	}
	err := &ShowFolderError{Title: "Lost", Why: "x"}
	if !errors.Is(err, ErrNoShowFolder) || !strings.Contains(err.Error(), "Can't tell which folder Lost lives in") {
		t.Errorf("ShowFolderError = %v", err)
	}
}

// The health check's list: every show with files the bug misplaced, from one pass over the
// database — matching what each show's own MisplacedFiles says.
func TestMisplacedShows(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	pe := f.show("Pechino Express",
		"/tv/Pechino Express/S12/a - S12E01.mkv",
		"/tv/S13/Season 12/a - S12E02.mkv",
		"/tv/S13/Season 13/a - S13E01.mkv")
	gh := f.show("Godfather of Harlem", "/tv/S01/Season 4/b - S04E01.mkv")
	f.show("Fine", "/tv/Fine/S01/c - S01E01.mkv")
	f.show("Teen Titans", "/tv/Teen Titans/Season 1/d - S01E01.mkv")
	f.show("Teen Titans Go!", "/tv/Teen Titans/Season 1/e - S01E01.mkv")

	got, err := f.svc.MisplacedShows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].SeriesID != pe || got[0].Files != 2 || got[0].NoFolder ||
		got[1].SeriesID != gh || got[1].Files != 1 || !got[1].NoFolder || got[0].Title != "Pechino Express" {
		t.Fatalf("MisplacedShows = %+v", got)
	}
	info, files, err := f.svc.MisplacedFiles(ctx, pe)
	if err != nil || info.Path != filepath.FromSlash("/tv/Pechino Express") || len(files) != 2 {
		t.Fatalf("MisplacedFiles = %+v %+v %v", info, files, err)
	}
	for _, m := range files {
		if !strings.Contains(m.Why, "named like a season folder") {
			t.Errorf("why = %q", m.Why)
		}
	}
}

// A show inside a category folder is read as v0.3.1 read it (and imported where v0.3.1
// imported it, <TV folder>/<show folder's name>), unless that is a #56 mistake: a
// season-named folder there is refused.
func TestShowFolderInsideACategory(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	frieren := f.show("Frieren", "/tv/Anime/Frieren/Season 1/Frieren - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, frieren); info.Unknown || info.Path != filepath.FromSlash("/tv/Anime/Frieren") {
		t.Errorf("Frieren = %+v", info)
	}
	if got := f.svc.ImportDir(ctx, Series{ID: frieren}); got != "" {
		t.Errorf("ImportDir = %q, want the TV folder, as v0.3.1", got)
	}
	flat := f.show("Sousou no Frieren", "/tv/Anime/Sousou no Frieren/Sousou no Frieren - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, flat); info.Unknown || info.Path != filepath.FromSlash("/tv/Anime/Sousou no Frieren") {
		t.Errorf("flat = %+v", info)
	}
	peppa := f.show("Peppa Pig", "/tv/Kids/Peppa Pig/S01/Peppa Pig - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, peppa); !info.Unknown {
		t.Errorf("Peppa Pig = %+v, want refused", info)
	}
	if got := f.svc.ShowFolderFor(filepath.FromSlash("/tv/Anime/Frieren/Season 1/Frieren - S01E01.mkv")); got != filepath.FromSlash("/tv/Anime/Frieren") {
		t.Errorf("ShowFolderFor = %s", got)
	}
}

// A stray folder with one of the show's files and a folder holding most of them that
// another show's files are in too: the show's folder is unknown, and none of its files
// are listed as misplaced.
func TestShowFolderOfSharedMajority(t *testing.T) {
	ctx := context.Background()
	f := newFolderFixture(t, "/tv")
	id := f.show("Teen Titans",
		"/tv/Teen Titans/Season 1/Teen Titans - S01E01.mkv",
		"/tv/Teen Titans/Season 1/Teen Titans - S01E02.mkv",
		"/tv/Teen Titans/Season 1/Teen Titans - S01E03.mkv",
		"/tv/Teen Titans (2003)/Season 2/Teen Titans - S02E01.mkv")
	f.show("Teen Titans Go!", "/tv/Teen Titans/Season 1/Teen Titans Go! - S01E01.mkv")
	if info := f.svc.ShowFolderOf(ctx, id); !info.Unknown || info.Path != "" {
		t.Fatalf("ShowFolderOf = %+v, want unknown", info)
	}
	if _, m, err := f.svc.MisplacedFiles(ctx, id); err != nil || len(m) != 0 {
		t.Errorf("misplaced = %+v (%v), want none", m, err)
	}
	got, err := f.svc.MisplacedShows(ctx)
	if err != nil || len(got) != 0 {
		t.Errorf("MisplacedShows = %+v (%v)", got, err)
	}
}

// Only the TV folder a folder is in names it "after the TV folder": with TV folders /tv and
// /media/Anime, /tv/Anime is a show's folder, /tv/tv isn't.
func TestFolderNamedAfterItsOwnTVFolder(t *testing.T) {
	f := newFolderFixture(t, "/tv")
	j := &folderJudge{svc: f.svc, roots: []string{filepath.FromSlash("/tv"), filepath.FromSlash("/media/Anime")}, others: func(string) []int64 { return nil }}
	if v := j.judge(filepath.FromSlash("/tv/Anime")); v.why != "" {
		t.Errorf("/tv/Anime: %s", v.why)
	}
	if v := j.judge(filepath.FromSlash("/tv/tv")); v.kind != unsoundRoot {
		t.Errorf("/tv/tv = %+v", v)
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

// A season folder template is honoured: "The Pitt Season 01" is a season folder in a
// library Arrmada names "{title} Season {season00}".
func TestShowFolderOfTemplateSeasonFolders(t *testing.T) {
	f := newFolderFixture(t, "/tv")
	f.svc.SetSeasonFolderMatcher(func(name string) bool { return strings.HasPrefix(name, "The Pitt Season ") })
	id := f.show("The Pitt", "/tv/The Pitt/The Pitt Season 01/The Pitt - S01E01.mkv")
	if info := f.svc.ShowFolderOf(context.Background(), id); info.Path != filepath.FromSlash("/tv/The Pitt") {
		t.Fatalf("ShowFolderOf = %+v", info)
	}
}
