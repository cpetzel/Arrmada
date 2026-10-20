package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

// #56 follow-up (napalmz): a season pack named in Italian ("Agent.Carter.Stagione.2…")
// for a #56-shaped show. The parser doesn't read "Stagione 2", so the download's name says
// no season; its episode files do. The check looks at them, and only them, and only for a
// #56-shaped show. Temp dirs only.

const carterPack = "Agent.Carter.Stagione.2.ITA.1080p.DLMux.DD5.1.x264-NovaRip"

// carterHarness is the reporter's library: <TV "Serie TV">/Marvel's Agent Carter with
// season folders "S01" and "S02", three episodes of season 1 and four of season 2
// recorded. Metadata lists three seasons of ten episodes. It returns the harness, the TV
// folder and the show's folder.
func carterHarness(t *testing.T) (*lifecycleHarness, string, string) {
	t.Helper()
	h := newLifecycleHarness(t)
	tv := filepath.Join(t.TempDir(), "Serie TV")
	if err := os.MkdirAll(tv, 0o755); err != nil {
		t.Fatal(err)
	}
	var seasons []metadata.SeasonDetails
	for sn := 1; sn <= 3; sn++ {
		var eps []metadata.EpisodeDetails
		for e := 1; e <= 10; e++ {
			eps = append(eps, metadata.EpisodeDetails{EpisodeNumber: e, AirDate: fmt.Sprintf("2015-0%d-%02d", sn, e)})
		}
		seasons = append(seasons, metadata.SeasonDetails{SeasonNumber: sn, Episodes: eps})
	}
	meta := &importMeta{d: metadata.SeriesDetails{
		SeriesResult:    metadata.SeriesResult{TMDBID: 61550, Title: "Marvel's Agent Carter", Year: 2015},
		NumberingSource: "tmdb",
		Seasons:         seasons,
	}}
	h.c.series = series.NewService(h.c.db, meta, tv, h.c.log)
	h.c.imp = library.NewImporter(tv, h.c.log)
	s, err := h.c.series.Add(h.ctx, 61550, "", true)
	if err != nil {
		t.Fatal(err)
	}
	h.seriesID = s.ID
	show := filepath.Join(tv, "Marvel's Agent Carter")
	for _, se := range [][2]int{{1, 1}, {1, 2}, {1, 3}, {2, 1}, {2, 2}, {2, 3}, {2, 4}} {
		p := filepath.Join(show, fmt.Sprintf("S%02d", se[0]), fmt.Sprintf("Marvel's Agent Carter - S%02dE%02d.mkv", se[0], se[1]))
		setEpisodeFile(t, h.c, s.ID, se[0], se[1], libFile(t, p), fmt.Sprintf("Agent.Carter.S%02dE%02d.ITA.720p", se[0], se[1]))
	}
	if info := h.c.series.ShowFolderOf(h.ctx, s.ID); !info.Shape56 {
		t.Fatalf("ShowFolderOf = %+v, want #56-shaped", info)
	}
	return h, tv, show
}

// carterDownload writes a download folder called name holding files (name → size).
func carterDownload(t *testing.T, name string, files map[string]int64) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, n := range files {
		sparseFile(t, filepath.Join(dir, f), n)
	}
	return dir
}

func carterShow(t *testing.T, h *lifecycleHarness) series.Series {
	t.Helper()
	s, err := h.c.series.Get(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const big = 60 << 20

func TestPlacementCheckReadsTheFilesOfA56ShapedShow(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]int64
		want  int    // the season the files land in; 0 = refused
		why   string // part of the refusal
	}{
		{"SxxEyy files", map[string]int64{"Agent.Carter.S02E05.ITA.1080p.mkv": big, "Agent.Carter.S02E06.ITA.1080p.mkv": big}, 2, ""},
		{"NxNN files", map[string]int64{"Agent Carter 2x05 ITA.mkv": big, "Agent Carter 2x06 ITA.mkv": big}, 2, ""},
		{"a sample and a small extra beside the episodes", map[string]int64{
			"Agent.Carter.S02E05.ITA.1080p.mkv": big, "Agent.Carter.S02E06.ITA.1080p.mkv": big,
			"Agent.Carter.Stagione.2.sample.mkv": big, "Extras/Behind the scenes.mkv": 1 << 20,
		}, 2, ""},
		{"seasons 1 and 2 mixed", map[string]int64{"Agent.Carter.S01E05.ITA.mkv": big, "Agent.Carter.S02E05.ITA.mkv": big}, 0, "its files are from seasons 1 and 2"},
		{"one file without a season", map[string]int64{"Agent.Carter.S02E05.ITA.mkv": big, "Agent.Carter.Stagione.2.Episodio.6.ITA.mkv": big}, 0, "Agent.Carter.Stagione.2.Episodio.6.ITA.mkv doesn't say which season and episode it is"},
		{"a season with no folder yet", map[string]int64{"Agent.Carter.S03E01.ITA.mkv": big, "Agent.Carter.S03E02.ITA.mkv": big}, 0, "it has no files of season 3 yet"},
		{"only a sample and an extra", map[string]int64{"Agent.Carter.S02E05.sample.mkv": big, "Agent.Carter.S02E06.Featurette.mkv": 1 << 20}, 0, "no video files were found in it"},
		{"an empty folder", map[string]int64{}, 0, "no video files were found in it"},
		{"only archives", map[string]int64{"novarip.rar": big}, 0, "no video files were found in it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tv, show := carterHarness(t)
			before := rvTree(tv)
			dl := carterDownload(t, carterPack, tc.files)
			s := carterShow(t, h)
			// v0.3.2 read the name alone and refused every one of these.
			if err := h.c.placementCheck(h.ctx, s, carterPack); !errors.Is(err, series.ErrNoShowFolder) {
				t.Fatalf("name-only check = %v, want v0.3.2's refusal", err)
			}
			err := h.c.placementCheckIn(h.ctx, s, carterPack, dl)
			if tc.want == 0 {
				if !errors.Is(err, series.ErrNoShowFolder) || !strings.Contains(err.Error(), tc.why) {
					t.Fatalf("check = %v, want refused saying %q", err, tc.why)
				}
				if !strings.HasPrefix(err.Error(), "Can't tell which folder Marvel's Agent Carter lives in: ") {
					t.Errorf("refusal = %q, want the show-folder refusal", err)
				}
				if after := rvTree(tv); after != before {
					t.Errorf("the library changed:\nbefore\n    %s\nafter\n    %s", before, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("check = %v, want allowed", err)
			}
			placed, _, unresolved, failed := h.c.importSeriesInto(h.ctx, s, dl, forceAll)
			if len(placed) != 2 || unresolved != 0 || failed != 0 {
				t.Fatalf("placed %v, unresolved %d, failed %d — want E05 and E06", placed, unresolved, failed)
			}
			d := filepath.Join(show, fmt.Sprintf("S%02d", tc.want))
			for _, ep := range []int{5, 6} {
				if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, tc.want, ep); filepath.Dir(cur.Path) != d {
					t.Errorf("S%02dE%02d landed at %q, want in %s", tc.want, ep, cur.Path, d)
				}
			}
			carterOnlyAdded(t, tv, before, d, 2)
		})
	}
}

// A single-file download: its name says no season, the file's own name does.
func TestPlacementCheckSingleFileDownload(t *testing.T) {
	h, tv, show := carterHarness(t)
	before := rvTree(tv)
	name := "Agent.Carter.Stagione.2.Episodio.5.ITA.1080p-NovaRip"
	file := filepath.Join(t.TempDir(), "Agent.Carter.S02E05.ITA.1080p-NovaRip.mkv")
	sparseFile(t, file, big)
	s := carterShow(t, h)
	if err := h.c.placementCheckIn(h.ctx, s, name, file); err != nil {
		t.Fatalf("check = %v, want allowed", err)
	}
	if placed, _, _, failed := h.c.importSeriesInto(h.ctx, s, file, forceAll); len(placed) != 1 || failed != 0 {
		t.Fatalf("placed %v, failed %d", placed, failed)
	}
	if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 2, 5); filepath.Dir(cur.Path) != filepath.Join(show, "S02") {
		t.Errorf("S02E05 landed at %q, want in S02", cur.Path)
	}
	carterOnlyAdded(t, tv, before, filepath.Join(show, "S02"), 1)
}

// A season folder holding another show's video isn't the season's own: refused, by the
// name (as v0.3.2) and by the files alike.
func TestPlacementCheckUncleanSeasonFolder(t *testing.T) {
	h, tv, show := carterHarness(t)
	libFile(t, filepath.Join(show, "S02", "Agents of SHIELD - S02E01.mkv"))
	before := rvTree(tv)
	dl := carterDownload(t, carterPack, map[string]int64{"Agent.Carter.S02E05.ITA.mkv": big, "Agent.Carter.S02E06.ITA.mkv": big})
	s := carterShow(t, h)
	if err := h.c.placementCheck(h.ctx, s, "Agent.Carter.S02E05.ITA.1080p-NovaRip"); !errors.Is(err, series.ErrNoShowFolder) {
		t.Fatalf("v0.3.2's name check = %v, want refused", err)
	}
	err := h.c.placementCheckIn(h.ctx, s, carterPack, dl)
	if !errors.Is(err, series.ErrNoShowFolder) || !strings.Contains(err.Error(), "holds a video that isn't one of the show's") {
		t.Fatalf("check = %v, want refused for the other show's file", err)
	}
	if after := rvTree(tv); after != before {
		t.Errorf("the library changed:\nbefore\n    %s\nafter\n    %s", before, after)
	}
}

// A show that isn't #56-shaped is checked exactly as v0.3.2 checked it: the download's
// files are never looked at, and the answer is its folder's, whatever the name.
func TestPlacementCheckOrdinaryShowUnchanged(t *testing.T) {
	type layout struct {
		name    string
		files   map[[2]int]string // tracked, relative to the TV folder
		other   string            // another tracked show's file, relative to the TV folder
		refused bool
	}
	layouts := []layout{
		{name: "Season NN folders", files: map[[2]int]string{{1, 1}: "Show/Season 01/Show - S01E01.mkv", {2, 1}: "Show/Season 02/Show - S02E01.mkv"}},
		{name: "flat show folder", files: map[[2]int]string{{1, 1}: "Show/Show - S01E01.mkv"}},
		{name: "no files yet", files: nil},
		{name: "folder shared with another show", files: map[[2]int]string{{1, 1}: "Shared/Season 1/Show - S01E01.mkv"}, other: "Shared/Season 2/Other - S02E01.mkv", refused: true},
	}
	downloads := []struct {
		name  string
		files map[string]int64
	}{
		{carterPack, map[string]int64{"Show.S02E05.mkv": big, "Show.S02E06.mkv": big}},
		{carterPack, map[string]int64{"Show.S01E05.mkv": big, "Show.S02E05.mkv": big}},
		{carterPack, map[string]int64{"Show.Stagione.2.Episodio.5.mkv": big}},
		{carterPack, map[string]int64{}},
		{"Show.S03E01.1080p.WEB-DL-GRP", map[string]int64{"Show.S03E01.1080p.WEB-DL-GRP.mkv": big}},
		{"Show.S02.1080p.WEB-DL-GRP", map[string]int64{"Show.S01E01.mkv": big}},
	}
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			h := newLifecycleHarness(t)
			tv := h.c.imp.TVRoot()
			for se, rel := range l.files {
				rvSet(t, h, se[0], se[1], rvFile(t, filepath.Join(tv, filepath.FromSlash(rel)), big))
			}
			if l.other != "" {
				res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (99, 'Other', 1)`)
				if err != nil {
					t.Fatal(err)
				}
				oid, _ := res.LastInsertId()
				setEpisodeFile(t, h.c, oid, 2, 1, libFile(t, filepath.Join(tv, filepath.FromSlash(l.other))), "Other.S02E01")
			}
			if info := h.c.series.ShowFolderOf(h.ctx, h.seriesID); info.Shape56 {
				t.Fatalf("ShowFolderOf = %+v, want an ordinary show", info)
			}
			s := carterShow(t, h)
			_, _, v032 := h.c.seriesPlacement(h.ctx, s) // what v0.3.2's !shape56 branch answered
			if (v032 != nil) != l.refused {
				t.Fatalf("seriesPlacement = %v, want refused=%v", v032, l.refused)
			}
			for _, d := range downloads {
				dl := carterDownload(t, d.name, d.files)
				byName := h.c.placementCheck(h.ctx, s, d.name)
				withFiles := h.c.placementCheckIn(h.ctx, s, d.name, dl)
				if fmt.Sprint(byName) != fmt.Sprint(v032) || fmt.Sprint(withFiles) != fmt.Sprint(v032) {
					t.Errorf("%s %v: name-only %v, with files %v — want both %v", d.name, d.files, byName, withFiles, v032)
				}
			}
		})
	}
}

// carterOnlyAdded asserts that the TV folder holds what it held before plus exactly n new
// files, all directly in d.
func carterOnlyAdded(t *testing.T, tv, before, d string, n int) {
	t.Helper()
	old := map[string]bool{}
	for _, f := range strings.Split(before, "\n    ") {
		old[f] = true
	}
	added := 0
	for _, f := range strings.Split(rvTree(tv), "\n    ") {
		if old[f] {
			delete(old, f)
			continue
		}
		added++
		if filepath.Dir(filepath.Join(tv, f)) != d {
			t.Errorf("%s was written outside %s", f, d)
		}
	}
	for f := range old {
		t.Errorf("%s is gone", f)
	}
	if added != n {
		t.Errorf("%d files were added, want %d:\n    %s", added, n, rvTree(tv))
	}
}

// The report, end to end: the sweep holds the Italian-named season-2 pack as "Doesn't
// match" (title matching is unchanged), and Import anyway puts its episodes into the
// show's own S02 folder, named like the others, touching nothing else. The same pack with
// files that say no episode is refused, and Map files… puts them into S02 — never into a
// season without a folder.
func TestAgentCarterStagionePack(t *testing.T) {
	hold := func(t *testing.T, h *lifecycleHarness, files ...string) (Review, string) {
		t.Helper()
		fs := map[string]int64{}
		for _, f := range files {
			fs[f] = big
		}
		dl := carterDownload(t, carterPack, fs)
		hash := h.completedTorrent(carterPack, seriesCategory, dl)
		h.seriesGrab(t, carterPack, hash)
		h.c.ImportSeriesDownloads(h.ctx)
		r := reviewFor(t, h.c, hash)
		if r.ReasonCode != ReasonMismatch || r.ExpectedID != h.seriesID {
			t.Fatalf("held as %s for %d (%q), want Doesn't match for the show", r.ReasonCode, r.ExpectedID, r.Reason)
		}
		return r, dl
	}

	t.Run("files say S02Exx: Import anyway lands them in S02", func(t *testing.T) {
		h, tv, show := carterHarness(t)
		before := rvTree(tv)
		files := []string{"Agent.Carter.S02E05.ITA.1080p.DLMux.DD5.1.x264-NovaRip.mkv", "Agent.Carter.S02E06.ITA.1080p.DLMux.DD5.1.x264-NovaRip.mkv"}
		r, dl := hold(t, h, files...)
		if after := rvTree(tv); after != before {
			t.Fatalf("the sweep placed something:\n    %s", after)
		}
		if err := h.c.ImportReview(h.ctx, r.ID, 0, ""); err != nil {
			t.Fatalf("Import anyway: %v", err)
		}
		d := filepath.Join(show, "S02")
		for _, ep := range []int{5, 6} {
			cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 2, ep)
			if filepath.Dir(cur.Path) != d {
				t.Errorf("S02E%02d landed at %q, want in %s", ep, cur.Path, d)
			}
			if want := fmt.Sprintf("Marvel's Agent Carter - S02E%02d", ep); !strings.HasPrefix(filepath.Base(cur.Path), want) {
				t.Errorf("S02E%02d is named %q, want it to start %q", ep, filepath.Base(cur.Path), want)
			}
		}
		carterOnlyAdded(t, tv, before, d, 2)
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(dl, f)); err != nil {
				t.Errorf("the download lost %s: %v", f, err)
			}
		}
		if got, _ := h.c.GetReview(h.ctx, r.ID); got.status == "pending" {
			t.Error("the review is still pending after the import")
		}
	})

	t.Run("files say no episode: refused, then Map files lands them in S02", func(t *testing.T) {
		h, tv, show := carterHarness(t)
		before := rvTree(tv)
		e5, e6 := "Agent.Carter.Stagione.2.Episodio.5.ITA.1080p.mkv", "Agent.Carter.Stagione.2.Episodio.6.ITA.1080p.mkv"
		r, _ := hold(t, h, e5, e6)
		err := h.c.ImportReview(h.ctx, r.ID, 0, "")
		if !errors.Is(err, series.ErrNoShowFolder) || !errors.Is(err, ErrNothingToImport) || !strings.Contains(err.Error(), "doesn't say which season and episode it is") {
			t.Fatalf("Import anyway = %v, want refused for the show's folder", err)
		}
		if after := rvTree(tv); after != before {
			t.Fatalf("a refused import placed something:\n    %s", after)
		}
		// A season without a folder of its own stays refused when mapped by hand.
		if _, err := h.c.ImportReviewMapped(h.ctx, r.ID, 0, []FileMapping{{RelPath: e5, Season: 3, Episodes: []int{5}}}); !errors.Is(err, series.ErrNoShowFolder) {
			t.Fatalf("mapping to season 3 = %v, want refused", err)
		}
		n, err := h.c.ImportReviewMapped(h.ctx, r.ID, 0, []FileMapping{
			{RelPath: e5, Season: 2, Episodes: []int{5}},
			{RelPath: e6, Season: 2, Episodes: []int{6}},
		})
		if err != nil || n != 2 {
			t.Fatalf("Map files = %d, %v", n, err)
		}
		d := filepath.Join(show, "S02")
		for _, ep := range []int{5, 6} {
			if cur := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 2, ep); filepath.Dir(cur.Path) != d {
				t.Errorf("S02E%02d landed at %q, want in %s", ep, cur.Path, d)
			}
		}
		carterOnlyAdded(t, tv, before, d, 2)
	})
}
