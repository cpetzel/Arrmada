package automation

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
	"github.com/tristenlammi/arrmada/internal/store"
)

// What the library scan and a rescan record from synthetic library trees (sparse files,
// never real media), for #81's single-digit NxE names ("11.22.63 - 1x8- …" in "S01").
//
// Every case is run three ways, each from a fresh database:
//
//	scan    — the TV library scan adopting the show folder (series.ScanLibrary);
//	rescan  — Refresh & rescan of the show added with no files yet (RescanSeries);
//	both    — the scan, then a rescan of what it recorded.
//
// and described as "S1E8=f7 …": each episode with a file → the index of that file.
//
// This file uses only what v0.3.3, main (931de3b3) and the fix share, so it runs unchanged
// on all three. v033 and main are what those two record (NXE_LAYOUT_PRINT=1 prints a run's
// readings instead of checking them); want is the fix. The only differences are the files
// the #81 rule reads that nothing read before.
type nxeLayoutCase struct {
	name    string
	title   string
	year    int
	anime   bool
	seasons map[int]int // season → episode count
	files   []string    // relative to the TV folder
	v033    [3]string   // scan, rescan, both
	main    [3]string
	want    [3]string
}

// nxeSame is one reading for all three modes.
func nxeSame(s string) [3]string { return [3]string{s, s, s} }

func nxeReporterFiles() []string {
	titles := []string{"The Rabbit Hole", "The Kill Floor", "Other Voices, Other Rooms", "The Eyes of Texas",
		"The Truth", "Happy Birthday, Lee Harvey Oswald", "Soldier Boy", "The Day in Question"}
	out := make([]string, len(titles))
	for i, t := range titles {
		out[i] = fmt.Sprintf("11.22.63 (2016)/S01/11.22.63 - 1x%d- %s [HDTVMux1080p Ita 5.1] NovaRip.mkv", i+1, t)
	}
	return out
}

var nxeLayoutCases = []nxeLayoutCase{
	{
		// The reporter's show: nothing was read; every file now is.
		name:  "issue 81 reporter, 11.22.63 (2016) S01 folder, 1xN- Title [tags]",
		title: "11.22.63", year: 2016, seasons: map[int]int{1: 8}, files: nxeReporterFiles(),
		v033: nxeSame(""),
		main: nxeSame(""),
		want: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S1E4=f3 S1E5=f4 S1E6=f5 S1E7=f6 S1E8=f7"),
	},
	{
		name:  "SxxEyy scene names in Season N folders",
		title: "Breaking Bad", year: 2008, seasons: map[int]int{1: 3, 2: 2}, files: []string{
			"Breaking Bad (2008)/Season 1/Breaking.Bad.S01E01.720p.BluRay.x264-GRP.mkv",
			"Breaking Bad (2008)/Season 1/Breaking.Bad.S01E02.720p.BluRay.x264-GRP.mkv",
			"Breaking Bad (2008)/Season 1/Breaking.Bad.S01E03.720p.BluRay.x264-GRP.mkv",
			"Breaking Bad (2008)/Season 2/Breaking.Bad.S02E01.1080p.BluRay.x264-GRP.mkv",
			"Breaking Bad (2008)/Season 2/Breaking Bad - S02E02 - Grilled.mkv",
		},
		v033: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S2E1=f3 S2E2=f4"),
		main: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S2E1=f3 S2E2=f4"),
		want: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S2E1=f3 S2E2=f4"),
	},
	{
		name:  "padded NxNN names (1x08), Season N folders and loose",
		title: "Parks and Recreation", year: 2009, seasons: map[int]int{1: 3, 6: 2}, files: []string{
			"Parks and Recreation (2009)/Season 1/Parks and Recreation - 1x01 - Pilot.mkv",
			"Parks and Recreation (2009)/Season 1/Parks and Recreation - 1x02 - Canvassing.mkv",
			"Parks and Recreation (2009)/Season 1/Parks and Recreation - 1x03 - The Reporter.mkv",
			"Parks and Recreation (2009)/Parks and Recreation - 6x01 & 6x02 - London.mkv",
		},
		v033: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S6E1=f3 S6E2=f3"),
		main: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S6E1=f3 S6E2=f3"),
		want: nxeSame("S1E1=f0 S1E2=f1 S1E3=f2 S6E1=f3 S6E2=f3"),
	},
	{
		// Only the last file is new: a lookalike set off by dashes AND in its season's
		// folder. 16x9 after the tags, [1x2] in brackets, 5.1x2 channels, 4x4 without
		// dashes, and the loose 16x9 / 4x4 with no season folder all stay unread.
		name:  "aspect ratios, dimensions and bracketed tokens",
		title: "Aspect Show", year: 2020, seasons: map[int]int{1: 3, 4: 4}, files: []string{
			"Aspect Show (2020)/Season 1/Aspect Show - S01E01 - 16x9 Cut.mkv",
			"Aspect Show (2020)/Season 1/Aspect Show 1080p 16x9.mkv",
			"Aspect Show (2020)/Season 1/Aspect Show [1x2] Extra.mkv",
			"Aspect Show (2020)/Season 1/Aspect Show 5.1x2.mkv",
			"Aspect Show (2020)/Season 4/Aspect Show 4x4 Offroad.mkv",
			"Aspect Show (2020)/Aspect Show - 16x9.mkv",
			"Aspect Show (2020)/Aspect Show - 4x4 - Offroad.mkv",
			"Aspect Show (2020)/Season 4/Aspect Show - 4x3 - Third.mkv",
		},
		v033: nxeSame("S1E1=f0"),
		main: nxeSame("S1E1=f0"),
		want: nxeSame("S1E1=f0 S4E3=f7"),
	},
	{
		// 1x5-1x6 is new; 1x7x8 and 1x9-10 (whose other episodes can't be told) stay unread.
		name:  "multi-episode names",
		title: "Multi Show", year: 2015, seasons: map[int]int{1: 10}, files: []string{
			"Multi Show (2015)/Season 1/Multi Show - S01E01-E02 - Pilot.mkv",
			"Multi Show (2015)/Season 1/Multi Show - 1x03-1x04 - Two.mkv",
			"Multi Show (2015)/Season 1/Multi Show - 1x5-1x6 - Three.mkv",
			"Multi Show (2015)/Season 1/Multi Show - 1x7x8 - Four.mkv",
			"Multi Show (2015)/Season 1/Multi Show - 1x9-10 - Five.mkv",
		},
		v033: nxeSame("S1E1=f0 S1E2=f0 S1E3=f1 S1E4=f1"),
		main: nxeSame("S1E1=f0 S1E2=f0 S1E3=f1 S1E4=f1"),
		want: nxeSame("S1E1=f0 S1E2=f0 S1E3=f1 S1E4=f1 S1E5=f2 S1E6=f2"),
	},
	{
		// The scan adds the show as a standard one (nothing here detects anime), so its
		// absolute files read only on a rescan. The 1x8 file is the one new reading.
		name:  "anime, absolute numbers",
		title: "Frieren", year: 2023, anime: true, seasons: map[int]int{1: 8}, files: []string{
			"Frieren (2023)/Season 1/[SubsPlease] Frieren - 05 (1080p) [ABCD1234].mkv",
			"Frieren (2023)/Season 1/[SubsPlease] Frieren - 06 (1080p) [ABCD1235].mkv",
			"Frieren (2023)/Season 1/Frieren S01E07.mkv",
			"Frieren (2023)/Season 1/Frieren - 1x8 - Eight.mkv",
		},
		v033: [3]string{"S1E7=f2", "S1E5=f0 S1E6=f1 S1E7=f2", "S1E5=f0 S1E6=f1 S1E7=f2"},
		main: [3]string{"S1E7=f2", "S1E5=f0 S1E6=f1 S1E7=f2", "S1E5=f0 S1E6=f1 S1E7=f2"},
		want: [3]string{"S1E7=f2 S1E8=f3", "S1E5=f0 S1E6=f1 S1E7=f2 S1E8=f3", "S1E5=f0 S1E6=f1 S1E7=f2 S1E8=f3"},
	},
	{
		name:  "daily show named by air date",
		title: "The Daily Show", year: 1996, seasons: map[int]int{2024: 3}, files: []string{
			"The Daily Show (1996)/Season 2024/The.Daily.Show.2024.10.03.1080p.WEB.h264-GRP.mkv",
			"The Daily Show (1996)/Season 2024/The Daily Show - 2024-10-04 - Guest.mkv",
		},
		v033: nxeSame(""),
		main: nxeSame(""),
		want: nxeSame(""),
	},
	{
		// 0x1 in Specials is new; 0x3 in Season 1 and the loose 0x4 stay unread.
		name:  "specials",
		title: "Show Specials", year: 2010, seasons: map[int]int{0: 4, 1: 3}, files: []string{
			"Show Specials (2010)/Specials/Show Specials - 0x1 - Christmas.mkv",
			"Show Specials (2010)/Specials/Show Specials - S00E02 - Pilot.mkv",
			"Show Specials (2010)/Season 1/Show Specials - 0x3 - Odd.mkv",
			"Show Specials (2010)/Show Specials - 0x4 - Loose.mkv",
			"Show Specials (2010)/Season 1/Show Specials - S01E01.mkv",
		},
		v033: nxeSame("S0E2=f1 S1E1=f4"),
		main: nxeSame("S0E2=f1 S1E1=f4"),
		want: nxeSame("S0E1=f0 S0E2=f1 S1E1=f4"),
	},
	{
		// The dashed 2x3 is new; "2x4" with no dashes and no season folder stays unread.
		name:  "flat show folder, no season folders",
		title: "Flat Show", year: 2018, seasons: map[int]int{2: 5}, files: []string{
			"Flat Show (2018)/Flat Show - 2x3 - Title.mkv",
			"Flat Show (2018)/Flat Show 2x4 Title.mkv",
			"Flat Show (2018)/Flat Show - S02E05 - Title.mkv",
		},
		v033: nxeSame("S2E5=f2"),
		main: nxeSame("S2E5=f2"),
		want: nxeSame("S2E3=f0 S2E5=f2"),
	},
	{
		// 1x2 in Season 1 is new; 2x1 in Season 1 (the folder says otherwise) stays unread.
		name:  "season folder disagreeing with the name",
		title: "Mismatch Show", year: 2019, seasons: map[int]int{1: 3, 2: 3}, files: []string{
			"Mismatch Show (2019)/Season 1/Mismatch Show - 2x1 - Title.mkv",
			"Mismatch Show (2019)/Season 1/Mismatch Show - 1x2 - Title.mkv",
			"Mismatch Show (2019)/Season 2/Mismatch Show - S02E02.mkv",
		},
		v033: nxeSame("S2E2=f2"),
		main: nxeSame("S2E2=f2"),
		want: nxeSame("S1E2=f1 S2E2=f2"),
	},
	{
		// No dashes, but the Stagione 1 folder says season 1. (v0.3.3 refused a rescan of a
		// scanned show kept in "Stagione N" folders, as its folder couldn't be told; main
		// reads them.)
		name:  "Stagione N folders, no dashes",
		title: "Gomorra", year: 2014, seasons: map[int]int{1: 4}, files: []string{
			"Gomorra (2014)/Stagione 1/Gomorra 1x3 Titolo [WEBDLMux1080p Ita].mkv",
			"Gomorra (2014)/Stagione 1/Gomorra S01E04 Titolo.mkv",
		},
		v033: [3]string{"S1E4=f1", "S1E4=f1", "refused"},
		main: nxeSame("S1E4=f1"),
		want: nxeSame("S1E3=f0 S1E4=f1"),
	},
	{
		// A newly read copy is a second file for its episode: a rescan keeps the better of
		// the two, exactly as it does for two SxxEyy copies (the control case below). The
		// other stays on disk and is listed as a duplicate; nothing is deleted.
		name:  "a 1080p 1x8 copy beside a 720p S01E08 file",
		title: "Dupe Show", year: 2012, seasons: map[int]int{1: 8}, files: []string{
			"Dupe Show (2012)/Season 1/Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv",
			"Dupe Show (2012)/Season 1/Dupe Show - 1x8 - Title [1080p].mkv",
		},
		v033: nxeSame("S1E8=f0"),
		main: nxeSame("S1E8=f0"),
		want: [3]string{"S1E8=f0", "S1E8=f1", "S1E8=f1"},
	},
	{
		name:  "control: a 1080p S01E08 copy beside a 720p S01E08 file",
		title: "Dupe Show", year: 2012, seasons: map[int]int{1: 8}, files: []string{
			"Dupe Show (2012)/Season 1/Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv",
			"Dupe Show (2012)/Season 1/Dupe Show - S01E08 - Title [1080p].mkv",
		},
		v033: [3]string{"S1E8=f0", "S1E8=f1", "S1E8=f1"},
		main: [3]string{"S1E8=f0", "S1E8=f1", "S1E8=f1"},
		want: [3]string{"S1E8=f0", "S1E8=f1", "S1E8=f1"},
	},
}

// nxeMeta answers one show for every search, and its listing.
type nxeMeta struct{ d metadata.SeriesDetails }

func (m *nxeMeta) Available() bool { return true }
func (m *nxeMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return []metadata.SeriesResult{m.d.SeriesResult}, nil
}
func (m *nxeMeta) GetSeries(context.Context, int) (*metadata.SeriesDetails, error) {
	cp := m.d
	return &cp, nil
}

func nxeDetails(tc nxeLayoutCase) metadata.SeriesDetails {
	d := metadata.SeriesDetails{SeriesResult: metadata.SeriesResult{TMDBID: 81, Title: tc.title, Year: tc.year}, Status: "Ended"}
	abs := 0
	for _, sn := range nxeSeasonNumbers(tc) {
		sd := metadata.SeasonDetails{SeasonNumber: sn}
		for e := 1; e <= tc.seasons[sn]; e++ {
			ed := metadata.EpisodeDetails{EpisodeNumber: e, Title: fmt.Sprintf("Episode %d", e), AirDate: "2020-01-01", Runtime: 45}
			if sn > 0 {
				abs++
				ed.AbsoluteNumber = abs
			}
			sd.Episodes = append(sd.Episodes, ed)
		}
		d.Seasons = append(d.Seasons, sd)
	}
	return d
}

func nxeSeasonNumbers(tc nxeLayoutCase) []int {
	var out []int
	for sn := range tc.seasons {
		out = append(out, sn)
	}
	sort.Ints(out)
	return out
}

type nxeEnv struct {
	c    *Coordinator
	ctx  context.Context
	root string
}

func newNxeEnv(t *testing.T, tc nxeLayoutCase) *nxeEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	for i, f := range tc.files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		fh, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := fh.Truncate(int64(200+i) << 20); err != nil { // sparse: no data written
			t.Fatal(err)
		}
		_ = fh.Close()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := &Coordinator{
		db: st.DB(), log: log, bus: eventbus.New(log),
		series: series.NewService(st.DB(), &nxeMeta{d: nxeDetails(tc)}, root, log),
		imp:    library.NewImporter(root, log),
	}
	return &nxeEnv{c: c, ctx: context.Background(), root: root}
}

// addBare adds the show with its listing and no files, as Add does before any import.
func (e *nxeEnv) addBare(t *testing.T, tc nxeLayoutCase) int64 {
	t.Helper()
	repo := series.NewRepo(e.c.db)
	typ := series.SeriesTypeStandard
	if tc.anime {
		typ = series.SeriesTypeAnime
	}
	sr, err := repo.Create(e.ctx, series.Series{TMDBID: 81, Title: tc.title, Year: tc.year, Monitored: true, Status: "Ended", SeriesType: typ})
	if err != nil {
		t.Fatal(err)
	}
	var seasons []series.Season
	for _, sd := range nxeDetails(tc).Seasons {
		sn := series.Season{SeasonNumber: sd.SeasonNumber, Monitored: true}
		for _, ed := range sd.Episodes {
			sn.Episodes = append(sn.Episodes, series.Episode{SeasonNumber: sd.SeasonNumber, EpisodeNumber: ed.EpisodeNumber,
				Title: ed.Title, AirDate: ed.AirDate, Runtime: ed.Runtime, Monitored: true, AbsoluteNumber: ed.AbsoluteNumber})
		}
		seasons = append(seasons, sn)
	}
	if err := repo.InsertSeasons(e.ctx, sr.ID, seasons); err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

// scanned runs the library scan and returns the show it added (0: none).
func (e *nxeEnv) scanned(t *testing.T, tc nxeLayoutCase) int64 {
	t.Helper()
	if _, err := e.c.series.ScanLibrary(e.ctx, "", nil); err != nil {
		t.Fatalf("scan: %v", err)
	}
	all, err := e.c.series.List(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.TMDBID == 81 {
			if tc.anime {
				if _, err := e.c.db.ExecContext(e.ctx, `UPDATE series SET series_type = ? WHERE id = ?`, series.SeriesTypeAnime, s.ID); err != nil {
					t.Fatal(err)
				}
			}
			return s.ID
		}
	}
	return 0
}

// describe is "S1E8=f7 …": each episode with a file, and which of the case's files it is.
func (e *nxeEnv) describe(t *testing.T, tc nxeLayoutCase, id int64) string {
	t.Helper()
	if id == 0 {
		return "(not added)"
	}
	s, err := e.c.series.Get(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, f := range tc.files {
		idx[filepath.Clean(filepath.Join(e.root, filepath.FromSlash(f)))] = i
	}
	var parts []string
	for _, sn := range s.Seasons {
		for _, ep := range sn.Episodes {
			if !ep.HasFile {
				continue
			}
			label := "?"
			if i, ok := idx[filepath.Clean(ep.FilePath)]; ok {
				label = fmt.Sprintf("f%d", i)
			}
			parts = append(parts, fmt.Sprintf("S%dE%d=%s", ep.SeasonNumber, ep.EpisodeNumber, label))
		}
	}
	return strings.Join(parts, " ")
}

func (e *nxeEnv) rescanned(t *testing.T, tc nxeLayoutCase, id int64) string {
	t.Helper()
	if id == 0 {
		return "(not added)"
	}
	if err := e.c.RescanSeries(e.ctx, id); err != nil {
		t.Logf("rescan refused: %v", err)
		return "refused"
	}
	return e.describe(t, tc, id)
}

func runNxeLayoutCase(t *testing.T, tc nxeLayoutCase) [3]string {
	var got [3]string
	e := newNxeEnv(t, tc)
	got[0] = e.describe(t, tc, e.scanned(t, tc))
	e = newNxeEnv(t, tc)
	got[1] = e.rescanned(t, tc, e.addBare(t, tc))
	e = newNxeEnv(t, tc)
	got[2] = e.rescanned(t, tc, e.scanned(t, tc))
	return got
}

func TestNxELayoutMatrix(t *testing.T) {
	printOnly := os.Getenv("NXE_LAYOUT_PRINT") != ""
	modes := [3]string{"scan", "rescan", "both"}
	for _, tc := range nxeLayoutCases {
		t.Run(tc.name, func(t *testing.T) {
			got := runNxeLayoutCase(t, tc)
			for i, m := range modes {
				if printOnly {
					t.Logf("NXE\t%s\t%s\t%s", tc.name, m, got[i])
					continue
				}
				if got[i] != tc.want[i] {
					t.Errorf("%s:\n got  %q\n want %q\n (v0.3.3 %q, main %q)", m, got[i], tc.want[i], tc.v033[i], tc.main[i])
				}
				if tc.want[i] != tc.main[i] || tc.want[i] != tc.v033[i] {
					t.Logf("%s changed: v0.3.3 %q, main %q → %q", m, tc.v033[i], tc.main[i], tc.want[i])
				}
			}
		})
	}
}
