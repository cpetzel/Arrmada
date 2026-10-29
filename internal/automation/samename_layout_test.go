package automation

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Same-named shows (#81): where a finished series download's files land, and which
// releases a search grabs, when the library has two shows with one title — Scrubs (2001)
// and the 2026 revival — next to control shows that share their name with nothing.
//
// Every case runs on a fresh store-backed library of sparse files (never real media):
//
//	Scrubs (2001)/S02/…         the 2001 show, "S02"-style season folders, S02E03 on disk
//	Scrubs (2026)/Season 01/…   the revival, "Season 01" folders, S01E01 on disk
//	House (2004)/Season 1/…     control: no same-named show, S02E03 on disk
//	Lost/S01/…                  control: no same-named show, no year in the folder
//
// and reports, for an import case, "held:<reason code>" when the download went to Needs you
// or the library files it added or changed ("+path" new, "~path" replaced); for a search
// case, the releases it grabbed.
//
// This file uses nothing newer than v0.3.3, so the same file runs on v0.3.3, on main
// before the fix and on the fix; the table in the change's report is those three runs.
// want is what the fix does; the runs on v0.3.3 and main are the baseline. A change may
// only turn a placement into "held" (a person decides) or a grab into none, and only for
// a show with a same-named sibling and a release that doesn't say which one it is.

// Air dates. The revival's season 2 aired in August 2026; the 2001 show's seasons long
// before.
var (
	scrubsOldSeasons = map[int]string{1: "2001-10-02", 2: "2002-09-26", 3: "2003-10-02"}
	scrubsNewSeasons = map[int]string{1: "2026-02-25", 2: "2026-08-05"}
	houseSeasons     = map[int]string{1: "2004-11-16", 2: "2005-09-13"}
	lostSeasons      = map[int]string{1: "2004-09-22"}
)

type sameNameRig struct {
	*stallHarness
	root                      string
	old, revival, house, lost int64
}

func newSameNameRig(t *testing.T) *sameNameRig {
	t.Helper()
	h := newStallHarness(t)
	root := t.TempDir()
	h.c.series = series.NewService(h.c.db, nil, root, h.c.log)
	h.c.imp = library.NewImporter(root, h.c.log)
	r := &sameNameRig{stallHarness: h, root: root}
	r.old = r.show(t, 101, "Scrubs", 2001, scrubsOldSeasons, "Scrubs (2001)", "S%02d",
		map[[2]int]string{{2, 3}: "Scrubs.S02E03.720p.HDTV.x264-OLD"})
	r.revival = r.show(t, 102, "Scrubs", 2026, scrubsNewSeasons, "Scrubs (2026)", "Season %02d",
		map[[2]int]string{{1, 1}: "Scrubs.2026.S01E01.720p.WEB-DL.x264-OLD"})
	r.house = r.show(t, 103, "House", 2004, houseSeasons, "House (2004)", "Season %d",
		map[[2]int]string{{2, 3}: "House.S02E03.720p.HDTV.x264-OLD"})
	r.lost = r.show(t, 104, "Lost", 2004, lostSeasons, "Lost", "S%02d",
		map[[2]int]string{{1, 1}: "Lost.S01E01.720p.HDTV.x264-OLD"})
	return r
}

// show adds a show with four weekly episodes per season and puts the files in have on
// disk (10 MB sparse) under folder/<season folder>.
func (r *sameNameRig) show(t *testing.T, tmdb int, title string, year int, seasons map[int]string, folder, seasonFmt string, have map[[2]int]string) int64 {
	t.Helper()
	repo := series.NewRepo(r.c.db)
	s, err := repo.Create(r.ctx, series.Series{TMDBID: tmdb, Title: title, Year: year, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	var ss []series.Season
	for sn, first := range seasons {
		start, _ := time.Parse("2006-01-02", first)
		season := series.Season{SeasonNumber: sn, Monitored: true}
		for e := 1; e <= 4; e++ {
			season.Episodes = append(season.Episodes, series.Episode{SeasonNumber: sn, EpisodeNumber: e,
				AirDate: start.AddDate(0, 0, 7*(e-1)).Format("2006-01-02"), Monitored: true})
		}
		ss = append(ss, season)
	}
	if err := repo.InsertSeasons(r.ctx, s.ID, ss); err != nil {
		t.Fatal(err)
	}
	for k, rel := range have {
		dir := filepath.Join(r.root, folder, fmt.Sprintf(seasonFmt, k[0]))
		path := filepath.Join(dir, rel+".mkv")
		sparse(t, path, 10<<20)
		if _, err := r.c.db.Exec(`UPDATE episodes SET has_file = 1, file_path = ?, size_bytes = ?, source_release = ?
			WHERE series_id = ? AND season_number = ? AND episode_number = ?`, path, 10<<20, rel, s.ID, k[0], k[1]); err != nil {
			t.Fatal(err)
		}
	}
	return s.ID
}

// libraryFiles maps every file under the library root to its size.
func (r *sameNameRig) libraryFiles(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	_ = filepath.Walk(r.root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(r.root, p)
			out[filepath.ToSlash(rel)] = fi.Size()
		}
		return nil
	})
	return out
}

// importCase is one finished download.
type importCase struct {
	name    string
	release string   // the torrent's name
	files   []string // its video files (60 MB sparse); release+".mkv" when empty
	grabFor string   // "old" | "revival" | "house" | "lost" | "" (not grabbed)
	manual  bool     // picked by hand from the interactive list (whole show)
	// published: the indexer's publish date recorded on the grab ("" none — and the column
	// doesn't exist before the fix, so on v0.3.3 and main it is never recorded).
	published string
	// added: when the client added the torrent (0: not reported).
	added time.Time
	// setup changes the revival's stored episodes first (sameNameRig.apply).
	setup string
	want  string
}

// apply changes the revival's stored metadata:
//
//	stale       its season 2 lists only E01–E02 (a listing from before the rest was announced)
//	bare        no seasons or episodes at all (just added, metadata not fetched yet)
//	unreadable  its episode rows can't be read (a value of the wrong type in one column)
func (r *sameNameRig) apply(t *testing.T, setup string) {
	t.Helper()
	var qs []string
	switch setup {
	case "":
		return
	case "stale":
		qs = []string{`DELETE FROM episodes WHERE series_id = ? AND season_number = 2 AND episode_number > 2`}
	case "bare":
		qs = []string{`DELETE FROM episodes WHERE series_id = ?`, `DELETE FROM seasons WHERE series_id = ?`}
	case "unreadable":
		qs = []string{`UPDATE episodes SET runtime = 'not a number' WHERE series_id = ?`}
	default:
		t.Fatalf("unknown setup %q", setup)
	}
	for _, q := range qs {
		if _, err := r.c.db.Exec(q, r.revival); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *sameNameRig) runImport(t *testing.T, c importCase) string {
	t.Helper()
	ids := map[string]int64{"old": r.old, "revival": r.revival, "house": r.house, "lost": r.lost}
	r.apply(t, c.setup)
	files := c.files
	if len(files) == 0 {
		files = []string{c.release + ".mkv"}
	}
	dir := filepath.Join(t.TempDir(), c.release)
	for _, f := range files {
		sparse(t, filepath.Join(dir, f), 60<<20)
	}
	hash := hashFor(c.release)
	if c.grabFor != "" {
		if _, err := r.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash)
			VALUES (?, ?, 'Fake', 60, 'series', ?)`, ids[c.grabFor], c.release, hash); err != nil {
			t.Fatal(err)
		}
		if c.manual {
			if _, err := r.c.db.Exec(`UPDATE grabs SET manual = 1, scope = '' WHERE info_hash = ?`, hash); err != nil {
				t.Fatal(err)
			}
		}
		if c.published != "" {
			// No such column before the fix: there the grab simply records none.
			_, _ = r.c.db.Exec(`UPDATE grabs SET published_at = ? WHERE info_hash = ?`, c.published, hash)
		}
	}
	torrent := map[string]any{
		"hash": hash, "name": c.release, "state": "uploading", "progress": 1.0, "amount_left": 0,
		"size": 1 << 30, "completed": 1 << 30, "category": seriesCategory, "content_path": dir,
	}
	if !c.added.IsZero() {
		torrent["added_on"] = c.added.Unix()
	}
	r.qbit.mu.Lock()
	r.qbit.torrents = append(r.qbit.torrents, torrent)
	r.qbit.mu.Unlock()
	r.c.downloads.InvalidateSnapshot()

	before := r.libraryFiles(t)
	r.c.ImportSeriesDownloads(r.ctx)
	r.c.ImportSeriesDownloads(r.ctx) // the second pass is the re-process that fills gaps

	var code string
	if err := r.c.db.QueryRow(`SELECT reason_code FROM import_reviews WHERE hash = ? AND status = 'pending'`, hash).Scan(&code); err == nil {
		return "held:" + code
	}
	after := r.libraryFiles(t)
	var changes []string
	for p, sz := range after {
		old, had := before[p]
		switch {
		case !had:
			changes = append(changes, "+"+p)
		case old != sz:
			changes = append(changes, "~"+p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changes = append(changes, "-"+p)
		}
	}
	sort.Strings(changes)
	if len(changes) == 0 {
		return "nothing"
	}
	return strings.Join(changes, " ")
}

func TestSameNameImportLayout(t *testing.T) {
	now := time.Now().UTC()
	yearsAgo := time.Date(2015, 6, 1, 0, 0, 0, 0, time.UTC)
	cases := []importCase{
		// The reporter's case: a yearless S02E03 grabbed for the 2001 show replaces its file.
		{name: "old: yearless S02E03 upgrade, posted 2015", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			published: "2015-06-01T00:00:00Z", added: now,
			want: ""},
		{name: "old: yearless S02E03 upgrade, posted after the revival's S02E03", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			published: "2026-09-01T00:00:00Z", added: now,
			want: "held:same_name"},
		{name: "old: yearless S02E03 upgrade, no publish date, added now", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			added: now, want: "held:same_name"},
		{name: "old: yearless S02E03 upgrade, no publish date, added 2015", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			added: yearsAgo, want: ""},
		{name: "old: yearless S02E04 (missing), no dates", release: "Scrubs.S02E04.1080p.WEB-DL.x264-GRP", grabFor: "old",
			want: "held:same_name"},
		// A season the revival doesn't list is ruled out only by the revival's premiere.
		{name: "old: yearless S03E01 (the revival lists no S03), added now", release: "Scrubs.S03E01.1080p.WEB-DL.x264-GRP", grabFor: "old",
			added: now, want: "held:same_name"},
		{name: "old: yearless S03E01 (the revival lists no S03), posted 2015", release: "Scrubs.S03E01.1080p.WEB-DL.x264-GRP", grabFor: "old",
			published: "2015-06-01T00:00:00Z", added: now, want: ""},
		// Stale or missing sibling metadata.
		{name: "old: yearless S02E03 posted 2026-09, revival lists S02 only to E02", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			published: "2026-09-01T00:00:00Z", added: now, setup: "stale", want: "held:same_name"},
		{name: "old: yearless S02E04 posted 2015, revival has no episodes yet", release: "Scrubs.S02E04.1080p.WEB-DL.x264-GRP", grabFor: "old",
			published: "2015-06-01T00:00:00Z", added: now, setup: "bare", want: "held:same_name"},
		{name: "old: yearless S02E03 posted 2015, revival's episodes unreadable", release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old",
			published: "2015-06-01T00:00:00Z", added: now, setup: "unreadable", want: "nothing"},
		// Multi-season packs.
		{name: "old: yearless S01-S03 pack, posted 2015", release: "Scrubs.S01-S03.1080p.BluRay.x264-PACK", grabFor: "old",
			files:     []string{"Scrubs.S01E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E04.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E04.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E04.1080p.BluRay.x264-PACK.mkv"},
			published: "2015-06-01T00:00:00Z", added: now, want: ""},
		{name: "old: yearless S01-S03 pack, posted after the revival premiered", release: "Scrubs.S01-S03.1080p.BluRay.x264-PACK", grabFor: "old",
			files:     []string{"Scrubs.S01E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S01E04.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E04.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E03.1080p.BluRay.x264-PACK.mkv", "Scrubs.S03E04.1080p.BluRay.x264-PACK.mkv"},
			published: "2026-05-01T00:00:00Z", added: now, want: "held:same_name"},
		{name: "old: S02E04 stating 2001", release: "Scrubs.2001.S02E04.1080p.WEB-DL.x264-GRP", grabFor: "old",
			added: now, want: ""},
		{name: "revival: yearless S02E01, posted after it aired", release: "Scrubs.S02E01.1080p.WEB-DL.x264-GRP", grabFor: "revival",
			published: "2026-08-06T00:00:00Z", added: now, want: "held:same_name"},
		{name: "revival: yearless S02E01, posted 2015 (the 2001 show's)", release: "Scrubs.S02E01.1080p.WEB-DL.x264-GRP", grabFor: "revival",
			published: "2015-06-01T00:00:00Z", added: now, want: "held:same_name"},
		{name: "revival: S02E02 stating 2026", release: "Scrubs.2026.S02E02.1080p.WEB-DL.x264-GRP", grabFor: "revival",
			added: now, want: ""},
		{name: "revival: yearless season 2 pack", release: "Scrubs.S02.1080p.WEB-DL.x264-GRP", grabFor: "revival",
			files: []string{"Scrubs.S02E01.1080p.WEB-DL.x264-GRP.mkv", "Scrubs.S02E02.1080p.WEB-DL.x264-GRP.mkv",
				"Scrubs.S02E03.1080p.WEB-DL.x264-GRP.mkv", "Scrubs.S02E04.1080p.WEB-DL.x264-GRP.mkv"},
			added: now, want: "held:same_name"},
		{name: "revival: yearless S02E03 picked by hand", release: "Scrubs.S02E03.1080p.WEB-DL.x264-HAND", grabFor: "revival",
			manual: true, added: now, want: ""},
		{name: "not grabbed: yearless S02E02", release: "Scrubs.S02E02.1080p.WEB-DL.x264-GRP",
			added: now, want: "held:unmatched"},
		{name: "not grabbed: S02E02 stating 2001", release: "Scrubs.2001.S02E02.1080p.WEB-DL.x264-GRP",
			added: now, want: ""},
		// Controls: shows without a same-named sibling.
		{name: "control House: yearless S02E03 upgrade, no dates", release: "House.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "house",
			want: ""},
		{name: "control House: yearless S02E04", release: "House.S02E04.1080p.WEB-DL.x264-GRP", grabFor: "house",
			added: now, want: ""},
		{name: "control House: season 1 pack", release: "House.S01.1080p.BluRay.x264-GRP", grabFor: "house",
			files: []string{"House.S01E01.1080p.BluRay.x264-GRP.mkv", "House.S01E02.1080p.BluRay.x264-GRP.mkv",
				"House.S01E03.1080p.BluRay.x264-GRP.mkv", "House.S01E04.1080p.BluRay.x264-GRP.mkv"},
			added: now, want: ""},
		{name: "control Lost: S01E02, posted 2015", release: "Lost.S01E02.1080p.BluRay.x264-GRP", grabFor: "lost",
			published: "2015-06-01T00:00:00Z", added: now, want: ""},
		{name: "control Lost: not grabbed S01E03", release: "Lost.S01E03.1080p.BluRay.x264-GRP",
			added: now, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newSameNameRig(t)
			got := r.runImport(t, c)
			t.Logf("LAYOUT|import|%s|%s", c.name, got)
			if c.want != "" && got != c.want {
				t.Errorf("%s: got %q, want %q", c.name, got, c.want)
			}
			if c.want == "" && strings.HasPrefix(got, "held:") {
				t.Errorf("%s: held (%s), want it placed as before", c.name, got)
			}
		})
	}
}

// searchCase is one search's results for a show missing some episodes.
type searchCase struct {
	name     string
	show     string // "old" | "revival" | "house"
	releases []indexer.Release
	want     []string // what the fix grabs
	setup    string   // sameNameRig.apply
}

func sameNameRel(title, published string) indexer.Release {
	r := indexer.Release{Title: title, Indexer: "Fake", DownloadURL: magnetFor(title), SizeBytes: 2 << 30, Seeders: 20,
		Transport: indexer.TransportTorrent}
	if published != "" {
		r.PublishedAt, _ = time.Parse(time.RFC3339, published)
	}
	return r
}

func TestSameNameSearchGrabs(t *testing.T) {
	cases := []searchCase{
		{"old: yearless S02E04 posted 2012", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "2012-01-01T00:00:00Z")},
			[]string{"Scrubs.S02E04.1080p.WEB-DL.x264-GRP"}, ""},
		{"old: yearless S02E04 posted after the revival's", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "2026-09-01T00:00:00Z")},
			nil, ""},
		{"old: yearless S02E04 with no publish date", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "")},
			nil, ""},
		{"old: S02E04 stating 2001, no date", "old", []indexer.Release{sameNameRel("Scrubs.2001.S02E04.1080p.WEB-DL.x264-GRP", "")},
			[]string{"Scrubs.2001.S02E04.1080p.WEB-DL.x264-GRP"}, ""},
		{"old: dated and undated side by side", "old", []indexer.Release{
			sameNameRel("Scrubs.S02E04.2160p.WEB-DL.x265-NEW", "2026-09-01T00:00:00Z"),
			sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-OLDER", "2010-01-01T00:00:00Z")},
			[]string{"Scrubs.S02E04.1080p.WEB-DL.x264-OLDER"}, ""},
		{"revival: yearless S02E03 posted 2015", "revival", []indexer.Release{sameNameRel("Scrubs.S02E03.1080p.WEB-DL.x264-GRP", "2015-01-01T00:00:00Z")},
			nil, ""},
		{"revival: yearless S02E03 posted after airing", "revival", []indexer.Release{sameNameRel("Scrubs.S02E03.1080p.WEB-DL.x264-GRP", "2026-08-20T00:00:00Z")},
			nil, ""},
		{"revival: S02E03 stating 2026", "revival", []indexer.Release{sameNameRel("Scrubs.2026.S02E03.1080p.WEB-DL.x264-GRP", "")},
			[]string{"Scrubs.2026.S02E03.1080p.WEB-DL.x264-GRP"}, ""},
		{"old: yearless S03E02 posted 2012 (the revival lists no S03)", "old", []indexer.Release{sameNameRel("Scrubs.S03E02.1080p.WEB-DL.x264-GRP", "2012-01-01T00:00:00Z")},
			[]string{"Scrubs.S03E02.1080p.WEB-DL.x264-GRP"}, ""},
		{"old: yearless S03E02 posted after the revival premiered (it lists no S03)", "old", []indexer.Release{sameNameRel("Scrubs.S03E02.1080p.WEB-DL.x264-GRP", "2026-09-01T00:00:00Z")},
			nil, ""},
		{"old: yearless S02E04 posted 2026-09, revival lists S02 only to E02", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "2026-09-01T00:00:00Z")},
			nil, "stale"},
		{"old: yearless S02E04 posted 2012, revival has no episodes yet", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "2012-01-01T00:00:00Z")},
			nil, "bare"},
		{"old: yearless S02E04 posted 2012, revival's episodes unreadable", "old", []indexer.Release{sameNameRel("Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "2012-01-01T00:00:00Z")},
			nil, "unreadable"},
		{"control House: yearless S02E04 no date", "house", []indexer.Release{sameNameRel("House.S02E04.1080p.WEB-DL.x264-GRP", "")},
			[]string{"House.S02E04.1080p.WEB-DL.x264-GRP"}, ""},
		{"control House: yearless S02E04 posted 1999", "house", []indexer.Release{sameNameRel("House.S02E04.1080p.WEB-DL.x264-GRP", "1999-01-01T00:00:00Z")},
			[]string{"House.S02E04.1080p.WEB-DL.x264-GRP"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newSameNameRig(t)
			ids := map[string]int64{"old": r.old, "revival": r.revival, "house": r.house}
			r.apply(t, c.setup)
			s, err := r.c.series.Get(r.ctx, ids[c.show])
			if err != nil {
				t.Fatal(err)
			}
			r.c.grabSeriesFrom(r.ctx, s, c.releases)
			var got []string
			for _, call := range r.qbit.callLog() {
				for _, rl := range c.releases {
					if call == "add "+rl.DownloadURL {
						got = append(got, rl.Title)
					}
				}
			}
			t.Logf("LAYOUT|search|%s|%s", c.name, strings.Join(got, ","))
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("%s: grabbed %v, want %v", c.name, got, c.want)
			}
		})
	}
}
