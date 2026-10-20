package automation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/store"
)

// yearWhenNeededNaming is the series scheme with "Add the year only when it's needed" on.
type yearWhenNeededNaming struct{}

func (yearWhenNeededNaming) SeriesNaming() library.SeriesNaming {
	return library.SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}
}

// folderLib is a temp-dir TV library with several shows, for renames across show folders.
type folderLib struct {
	c    *Coordinator
	ctx  context.Context
	root string
}

func newFolderLib(t *testing.T) *folderLib {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	imp := library.NewImporter(root, log)
	imp.SetSeriesNaming(yearWhenNeededNaming{})
	// The worst case for a rename: the check says no show ever needs its year.
	imp.SetSeriesYearNeededFunc(func(string, int, string) bool { return false })
	c := &Coordinator{db: st.DB(), log: log, bus: eventbus.New(log), series: series.NewService(st.DB(), nil, root, log), imp: imp}
	return &folderLib{c: c, ctx: context.Background(), root: root}
}

// show adds a show with season 1 episodes 1..n.
func (l *folderLib) show(t *testing.T, tmdb int, title string, year, n int) int64 {
	t.Helper()
	repo := series.NewRepo(l.c.db)
	sr, err := repo.Create(l.ctx, series.Series{TMDBID: tmdb, Title: title, Year: year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	var eps []series.Episode
	for e := 1; e <= n; e++ {
		eps = append(eps, series.Episode{SeasonNumber: 1, EpisodeNumber: e, AirDate: "2021-01-01"})
	}
	if err := repo.InsertSeasons(l.ctx, sr.ID, []series.Season{{SeasonNumber: 1, Monitored: true, Episodes: eps}}); err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

// file writes a file under the TV root and points the episode at it.
func (l *folderLib) file(t *testing.T, id int64, episode int, rel string) string {
	t.Helper()
	p := filepath.Join(l.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.c.series.MarkEpisodeImported(l.ctx, id, 1, episode, p, int64(len(rel))); err != nil {
		t.Fatal(err)
	}
	return p
}

// A rename names files inside the folder the show already has, so turning on "Add the year
// only when it's needed" — even with every show "not needing" it — never moves a show out
// of its folder. "Dark Matter (2015)" and "Dark Matter (2024)" stay apart: neither can drop
// its year and take the other's place, and no year-less "Dark Matter" folder appears.
func TestSeriesRenameKeepsShowFoldersWithYearWhenNeeded(t *testing.T) {
	l := newFolderLib(t)
	older := l.show(t, 100, "Dark Matter", 2015, 2)
	newer := l.show(t, 200, "Dark Matter", 2024, 2)
	settled := l.show(t, 300, "Breaking Bad", 2008, 1)
	l.file(t, older, 1, "Dark Matter (2015)/Season 1/dark.matter.2015.s01e01.mkv")
	l.file(t, older, 2, "Dark Matter (2015)/Season 1/dark.matter.2015.s01e02.mkv")
	l.file(t, newer, 1, "Dark Matter (2024)/Season 1/Dark.Matter.2024.S01E01.mkv")
	l.file(t, newer, 2, "Dark Matter (2024)/Season 1/Dark.Matter.2024.S01E02.mkv")
	// Already named by the scheme in force before the setting was turned on.
	l.file(t, settled, 1, "Breaking Bad (2008)/Season 1/Breaking Bad - S01E01.mkv")

	// Turning the setting on renames nothing by itself: a show whose files already sit at
	// their names has nothing in its preview.
	if items, err := l.c.SeriesRenamePreview(l.ctx, settled); err != nil || len(items) != 0 {
		t.Fatalf("Breaking Bad preview = %+v, %v; want nothing to rename", items, err)
	}

	folders := map[string]int64{}
	for _, id := range []int64{older, newer} {
		items, err := l.c.SeriesRenamePreview(l.ctx, id)
		if err != nil || len(items) != 2 {
			t.Fatalf("preview for %d = %+v, %v", id, items, err)
		}
		for _, it := range items {
			from, to := series.ShowFolder(it.From), series.ShowFolder(it.To)
			if from != to {
				t.Errorf("preview moves %q from show folder %q to %q", filepath.Base(it.From), from, to)
			}
			if prev, ok := folders[to]; ok && prev != id {
				t.Fatalf("two shows would share the folder %q", to)
			}
			folders[to] = id
		}
		res, err := l.c.SeriesRename(l.ctx, id, items)
		if err != nil || res.Moved != 2 || len(res.Skipped) != 0 {
			t.Fatalf("rename %d: %+v, %v", id, res, err)
		}
	}
	if _, err := os.Stat(filepath.Join(l.root, "Dark Matter")); !os.IsNotExist(err) {
		t.Errorf("a year-less Dark Matter folder appeared (err %v)", err)
	}
	for _, f := range []string{"Dark Matter (2015)", "Dark Matter (2024)"} {
		if got := len(mustGlob(t, filepath.Join(l.root, f, "Season 1", "*.mkv"))); got != 2 {
			t.Errorf("%s holds %d episode files, want 2", f, got)
		}
	}
}

// A rename never merges two shows. A show split over two folders is gathered into the one
// that is its own — the show folder resolver picks "Teen Titans Go! (2013)", never the
// folder Teen Titans' files are in — so the stray episode moves OUT of Teen Titans' folder,
// and nothing moves into it. (The guard that refuses a move into another show's folder is
// tested on its own in series_rename_guard_test.go.)
func TestSeriesRenameNeverGathersIntoAnotherShowsFolder(t *testing.T) {
	l := newFolderLib(t)
	titans := l.show(t, 100, "Teen Titans", 2003, 1)
	goShow := l.show(t, 200, "Teen Titans Go!", 2013, 2)
	l.file(t, titans, 1, "Teen Titans/Season 1/Teen Titans - S01E01.mkv")
	// Teen Titans Go! has one file in Teen Titans' folder and one in its own.
	l.file(t, goShow, 1, "Teen Titans/Season 1/Teen Titans Go - S01E01.mkv")
	l.file(t, goShow, 2, "Teen Titans Go! (2013)/Season 1/ttg.s01e02.mkv")

	items, err := l.c.SeriesRenamePreview(l.ctx, goShow)
	if err != nil {
		t.Fatal(err)
	}
	titansDir := filepath.Join(l.root, "Teen Titans")
	for _, it := range items {
		if top, _ := firstFolderUnder(l.root, it.To); top == "Teen Titans" && it.Conflict == "" {
			t.Errorf("preview moves %s into Teen Titans' folder", it.From)
		}
	}
	res, err := l.c.SeriesRename(l.ctx, goShow, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, sk := range res.Skipped {
		if top, _ := firstFolderUnder(l.root, sk.To); top != "Teen Titans" {
			t.Errorf("a move inside or into the show's own folder was skipped: %+v", sk)
		}
	}
	if got := mustGlob(t, filepath.Join(titansDir, "Season 1", "*.mkv")); len(got) > 2 {
		t.Errorf("Teen Titans' season folder gained files: %v", got)
	}
	if got := mustGlob(t, filepath.Join(titansDir, "Season 1", "Teen Titans - S01E01.mkv")); len(got) != 1 {
		t.Errorf("Teen Titans' own episode is gone: %v", got)
	}
}

// A rename inside a show's own folder is never refused by that check, even when the show
// shares the folder already (that's reported at import; moving files out is the user's call).
func TestSeriesRenameInsideOwnFolderNotRefused(t *testing.T) {
	l := newFolderLib(t)
	id := l.show(t, 300, "Breaking Bad", 2008, 1)
	l.file(t, id, 1, "Breaking Bad (2008)/Season 1/breaking.bad.s01e01.mkv")
	items, err := l.c.SeriesRenamePreview(l.ctx, id)
	if err != nil || len(items) != 1 || items[0].Conflict != "" {
		t.Fatalf("preview = %+v, %v", items, err)
	}
	if want := filepath.Join(l.root, "Breaking Bad (2008)", "Season 1", "Breaking Bad - S01E01.mkv"); items[0].To != want {
		t.Errorf("target %q, want %q", items[0].To, want)
	}
}

func mustGlob(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
