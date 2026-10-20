package series

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/store"
)

// tmdbSays is a TMDB search that answers with results, or fails with err.
func tmdbSays(err error, results ...metadata.SeriesResult) func(context.Context, string) ([]metadata.SeriesResult, error) {
	return func(context.Context, string) ([]metadata.SeriesResult, error) { return results, err }
}

// The year-when-needed decision, rule by rule (folderYearNeeded).
func TestFolderYearNeeded(t *testing.T) {
	ctx := context.Background()
	self := Series{ID: 2, TMDBID: 200, Title: "Dark Matter", Year: 2024}
	onlySelf := tmdbSays(nil, metadata.SeriesResult{TMDBID: 200, Title: "Dark Matter", Year: 2024})

	cases := []struct {
		name     string
		q        folderYearQuery
		library  []Series
		folders  []string // folders made directly in the TV root
		files    []string // plain files made in the TV root
		own      string   // the show's own folder, by name in the TV root
		yearless string   // the year-less folder name the scheme gives the show
		tracked  []string // folders (by name in the TV root) another series has files in
		symlinks []string // dangling symlinks made in the TV root
		noRoot   bool     // the TV root can't be read
		search   func(context.Context, string) ([]metadata.SeriesResult, error)
		want     bool
	}{
		{name: "nothing shares the title", library: []Series{self}, search: onlySelf, want: false},
		{name: "library duplicate", library: []Series{{ID: 1, TMDBID: 100, Title: "Dark Matter", Year: 2015}, self}, search: onlySelf, want: true},
		{name: "library duplicate, different case and punctuation", library: []Series{{ID: 1, Title: "DARK MATTER!", Year: 2015}, self}, search: onlySelf, want: true},
		{name: "library duplicate with The folded", q: folderYearQuery{Title: "The Office", Year: 2005, SelfID: 9, SelfTMDB: 2316},
			library: []Series{{ID: 9, TMDBID: 2316, Title: "The Office", Year: 2005}, {ID: 8, Title: "Office", Year: 2001}},
			search:  tmdbSays(nil, metadata.SeriesResult{TMDBID: 2316, Title: "The Office", Year: 2005}), want: true},
		{name: "library duplicate spelled with &", q: folderYearQuery{Title: "Love & Death", Year: 2023, SelfID: 5, SelfTMDB: 55},
			library: []Series{{ID: 5, TMDBID: 55, Title: "Love & Death", Year: 2023}, {ID: 6, Title: "Love and Death", Year: 2019}},
			search:  tmdbSays(nil), want: true},
		{name: "similar titles don't count", library: []Series{self, {ID: 3, Title: "Dark Matters", Year: 2011}, {ID: 4, Title: "Dark Matter Origins", Year: 2020}},
			folders: []string{"Dark Matters (2011)", "Dark"}, search: tmdbSays(nil, metadata.SeriesResult{TMDBID: 300, Title: "Dark Matters: Twisted But True", Year: 2011}), want: false},
		{name: "untracked folder on disk", library: []Series{self}, folders: []string{"Dark Matter"}, search: onlySelf, want: true},
		{name: "untracked folder, other case", library: []Series{self}, folders: []string{"dark matter"}, search: onlySelf, want: true},
		{name: "untracked folder carrying a year", library: []Series{self}, folders: []string{"Dark Matter (2015)"}, search: onlySelf, want: true},
		{name: "untracked folder carrying a Plex id", library: []Series{self}, folders: []string{"Dark Matter {tvdb-292174}"}, search: onlySelf, want: true},
		{name: "a plain file with the name isn't a folder", library: []Series{self}, files: []string{"Dark Matter"}, search: onlySelf, want: false},
		{name: "the show's own folder isn't a duplicate of itself", library: []Series{self}, folders: []string{"Dark Matter"}, own: "Dark Matter", search: onlySelf, want: false},
		{name: "own folder excluded, another still counts", library: []Series{self}, folders: []string{"Dark Matter", "Dark Matter (2015)"}, own: "Dark Matter", search: onlySelf, want: true},
		{name: "TMDB duplicate", library: []Series{self},
			search: tmdbSays(nil, metadata.SeriesResult{TMDBID: 100, Title: "Dark Matter", Year: 2015}, metadata.SeriesResult{TMDBID: 200, Title: "Dark Matter", Year: 2024}), want: true},
		{name: "TMDB duplicate, different case", library: []Series{self},
			search: tmdbSays(nil, metadata.SeriesResult{TMDBID: 100, Title: "dark matter", Year: 2015}, metadata.SeriesResult{TMDBID: 200, Title: "Dark Matter", Year: 2024}), want: true},
		{name: "TMDB lists only fuzzy matches", library: []Series{self},
			search: tmdbSays(nil, metadata.SeriesResult{TMDBID: 200, Title: "Dark Matter", Year: 2024}, metadata.SeriesResult{TMDBID: 301, Title: "Dark Materials", Year: 2019}), want: false},
		{name: "TMDB down keeps the year", library: []Series{self}, search: tmdbSays(errors.New("tmdb: HTTP 503")), want: true},
		{name: "TMDB not set up keeps the year", library: []Series{self}, search: nil, want: true},
		{name: "not in the library yet: TMDB's same-year entry is the show itself", q: folderYearQuery{Title: "Dark Matter", Year: 2024},
			search: onlySelf, want: false},
		{name: "not in the library yet: a library show of another year counts", q: folderYearQuery{Title: "Dark Matter", Year: 2024},
			library: []Series{{ID: 1, Title: "Dark Matter", Year: 2015}}, search: onlySelf, want: true},
		{name: "no year to compare keeps it", q: folderYearQuery{Title: "Dark Matter"}, library: []Series{self}, search: onlySelf, want: true},
		{name: "an unreadable TV folder keeps the year", library: []Series{self}, noRoot: true, search: onlySelf, want: true},
		{name: "an entry that can't be read keeps the year", library: []Series{self}, symlinks: []string{"Dark Matter"}, search: onlySelf, want: true},
		{name: "the exact year-less name, any case, keeps the year", library: []Series{self}, yearless: "TV - Dark Matter",
			folders: []string{"tv - dark matter"}, search: onlySelf, want: true},
		{name: "the exact year-less name as the show's own folder doesn't", library: []Series{self}, yearless: "TV - Dark Matter",
			folders: []string{"TV - Dark Matter"}, own: "TV - Dark Matter", search: onlySelf, want: false},
		{name: "another series' files recorded under the year-less name", library: []Series{self}, yearless: "TV - Dark Matter",
			tracked: []string{"TV - Dark Matter"}, search: onlySelf, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range c.folders {
				if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range c.files {
				if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, l := range c.symlinks {
				if err := os.Symlink(filepath.Join(root, "gone", "nowhere"), filepath.Join(root, l)); err != nil {
					t.Skipf("no symlinks here: %v", err)
				}
			}
			q := c.q
			if q.Title == "" {
				q = folderYearQuery{Title: self.Title, Year: self.Year, SelfID: self.ID, SelfTMDB: self.TMDBID}
			}
			q.Yearless = c.yearless
			if c.own != "" {
				q.OwnFolder = filepath.Join(root, c.own)
			}
			tracked := func(dir string) bool {
				for _, n := range c.tracked {
					if dir == filepath.Join(root, n) {
						return true
					}
				}
				return false
			}
			roots := []string{root}
			if c.noRoot {
				roots = append(roots, filepath.Join(root, "unmounted"))
			}
			if got, why := folderYearNeeded(ctx, q, c.library, roots, tracked, c.search); got != c.want {
				t.Errorf("needed = %v (%s), want %v", got, why, c.want)
			}
		})
	}
}

// yearMeta is a metadata provider whose series search the test controls and counts.
type yearMeta struct {
	results []metadata.SeriesResult
	err     error
	calls   int
}

func (m *yearMeta) Available() bool { return true }
func (m *yearMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	m.calls++
	return m.results, m.err
}
func (m *yearMeta) GetSeries(context.Context, int) (*metadata.SeriesDetails, error) {
	return nil, metadata.ErrNotFound
}

type folderYearFixture struct {
	svc  *Service
	meta *yearMeta
	root string
	ctx  context.Context
}

func newFolderYearFixture(t *testing.T) *folderYearFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := &yearMeta{}
	root := t.TempDir()
	return &folderYearFixture{svc: NewService(st.DB(), m, root, slog.New(slog.NewTextHandler(io.Discard, nil))), meta: m, root: root, ctx: context.Background()}
}

// show adds a library show with a one-episode season 1.
func (f *folderYearFixture) show(t *testing.T, tmdb int, title string, year int) Series {
	t.Helper()
	sr, err := f.svc.repo.Create(f.ctx, Series{TMDBID: tmdb, Title: title, Year: year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	eps := []Episode{{SeasonNumber: 1, EpisodeNumber: 1, AirDate: "2024-01-01"}, {SeasonNumber: 1, EpisodeNumber: 2, AirDate: "2024-01-08"}}
	if err := f.svc.repo.InsertSeasons(f.ctx, sr.ID, []Season{{SeasonNumber: 1, Monitored: true, Episodes: eps}}); err != nil {
		t.Fatal(err)
	}
	return sr
}

// importer is a TV importer with the setting on and this fixture's check installed.
func (f *folderYearFixture) importer(fy *FolderYears) *library.Importer {
	im := library.NewImporter(f.root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	im.SetSeriesNaming(yearNaming{})
	im.SetSeriesYearNeededFunc(func(title string, year int, yearless string) bool { return fy.Needed(f.ctx, title, year, yearless) })
	return im
}

type yearNaming struct{}

func (yearNaming) SeriesNaming() library.SeriesNaming {
	return library.SeriesNaming{Folder: "{title} ({year})", YearWhenNeeded: true}
}

// importEp imports a fake episode of sr through im the way an automatic import does: into
// the show's existing folder when it has one, else a new one. Returns the show folder name.
func (f *folderYearFixture) importEp(t *testing.T, im *library.Importer, sr Series, episode int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), fmt.Sprintf("Show.S01E%02d.1080p.WEB-DL.mkv", episode))
	if err := os.WriteFile(src, []byte(sr.Title), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := f.svc.ExistingFolderName(f.ctx, sr.ID)
	if folder == "" {
		folder = im.NewSeriesFolderName(sr.Title, sr.Year) // named once per placement
	}
	ei, ok, err := im.ImportEpisodeInto(folder, sr.Title, sr.Year, src)
	if err != nil || !ok {
		t.Fatalf("import %s E%d: ok=%v err=%v", sr.Title, episode, ok, err)
	}
	if err := f.svc.MarkEpisodeImported(f.ctx, sr.ID, 1, episode, ei.TargetPath, ei.SizeBytes); err != nil {
		t.Fatal(err)
	}
	return filepath.Base(ShowFolder(ei.TargetPath))
}

// Two shows that share a title can never both drop the year and land in one folder: each
// sees the other in the library, so both keep it — even with TMDB saying nothing.
func TestFolderYearsSharedTitleNeverCollides(t *testing.T) {
	f := newFolderYearFixture(t)
	older := f.show(t, 100, "Dark Matter", 2015)
	newer := f.show(t, 200, "Dark Matter", 2024)
	alone := f.show(t, 300, "Breaking Bad", 2008)
	im := f.importer(NewFolderYears(f.svc))

	got := map[string]string{}
	for _, sr := range []Series{older, newer, alone} {
		got[fmt.Sprintf("%s %d", sr.Title, sr.Year)] = f.importEp(t, im, sr, 1)
	}
	want := map[string]string{"Dark Matter 2015": "Dark Matter (2015)", "Dark Matter 2024": "Dark Matter (2024)", "Breaking Bad 2008": "Breaking Bad"}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s landed in %q, want %q", k, got[k], w)
		}
	}
	if got["Dark Matter 2015"] == got["Dark Matter 2024"] {
		t.Fatalf("two shows share the folder %q", got["Dark Matter 2015"])
	}
}

// Every episode of a new show lands in the folder its first episode made. The second
// episode's check must not mistake that folder for somebody else's — even with the
// remembered answer gone — and must not change its mind when TMDB comes back.
func TestFolderYearsDecidedOnce(t *testing.T) {
	t.Run("year dropped", func(t *testing.T) {
		f := newFolderYearFixture(t)
		sr := f.show(t, 300, "Breaking Bad", 2008)
		f.meta.results = []metadata.SeriesResult{{TMDBID: 300, Title: "Breaking Bad", Year: 2008}}
		first := f.importEp(t, f.importer(NewFolderYears(f.svc)), sr, 1)
		if first != "Breaking Bad" {
			t.Fatalf("first episode in %q", first)
		}
		// A fresh check (nothing remembered): the "Breaking Bad" folder on disk is the
		// show's own now, so it doesn't count as a duplicate.
		fresh := NewFolderYears(f.svc)
		if fresh.Needed(f.ctx, "Breaking Bad", 2008, "Breaking Bad") {
			t.Error("the show's own folder was counted as another show's")
		}
		// And the import follows the existing folder anyway.
		if second := f.importEp(t, f.importer(fresh), sr, 2); second != first {
			t.Errorf("second episode in %q, first in %q", second, first)
		}
	})
	t.Run("TMDB down keeps the year, and it stays kept", func(t *testing.T) {
		f := newFolderYearFixture(t)
		sr := f.show(t, 300, "Breaking Bad", 2008)
		f.meta.err = errors.New("tmdb: HTTP 503")
		fy := NewFolderYears(f.svc)
		im := f.importer(fy)
		if first := f.importEp(t, im, sr, 1); first != "Breaking Bad (2008)" {
			t.Fatalf("with TMDB down the first episode went to %q, want the year kept", first)
		}
		f.meta.err = nil
		f.meta.results = []metadata.SeriesResult{{TMDBID: 300, Title: "Breaking Bad", Year: 2008}}
		if !fy.Needed(f.ctx, "Breaking Bad", 2008, "Breaking Bad") {
			t.Error("the answer changed while the show's first import was still in progress")
		}
		// A folder-less second import (as when a pack's folder was looked up before the
		// first episode landed) still joins the first.
		if p := im.EpisodeTargetIn("", sr.Title, sr.Year, 1, 2, "x.S01E02.mkv", ".mkv"); filepath.Base(ShowFolder(p)) != "Breaking Bad (2008)" {
			t.Errorf("second episode would go to %q", p)
		}
	})
}

// TMDB is asked once per title, not once per episode; a failure isn't remembered as an
// answer, and the remembered decision expires.
func TestFolderYearsCachesTMDB(t *testing.T) {
	f := newFolderYearFixture(t)
	f.show(t, 300, "Breaking Bad", 2008)
	f.meta.results = []metadata.SeriesResult{{TMDBID: 300, Title: "Breaking Bad", Year: 2008}}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	fy := NewFolderYears(f.svc)
	fy.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if fy.Needed(f.ctx, "Breaking Bad", 2008, "Breaking Bad") {
			t.Fatal("needed with nothing sharing the title")
		}
	}
	if f.meta.calls != 1 {
		t.Errorf("TMDB asked %d times, want 1", f.meta.calls)
	}
	now = now.Add(folderYearMemoTTL + time.Minute) // the decision expires; TMDB's answer is still cached
	fy.Needed(f.ctx, "Breaking Bad", 2008, "Breaking Bad")
	if f.meta.calls != 1 {
		t.Errorf("TMDB asked %d times after the decision expired, want its cached answer used", f.meta.calls)
	}
	// A library change is seen once the remembered decision expires.
	f.show(t, 301, "Breaking Bad", 1995)
	now = now.Add(folderYearMemoTTL + time.Minute)
	if !fy.Needed(f.ctx, "Breaking Bad", 2008, "Breaking Bad") {
		t.Error("a second Breaking Bad in the library didn't make the year needed")
	}
	// A failed search isn't cached as an answer: the next check asks again.
	fy2 := NewFolderYears(f.svc)
	f.meta.err, f.meta.calls = errors.New("timeout"), 0
	fy2.searchTMDB(f.ctx, "Lost")
	fy2.searchTMDB(f.ctx, "Lost")
	if f.meta.calls != 2 {
		t.Errorf("a failed search was cached: asked %d times, want 2", f.meta.calls)
	}
}
