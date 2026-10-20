package automation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// Reviews of the #56 fix: layouts j, k, p, o, r, v, w, x, y and an ongoing #56-shaped show.
// What every consumer of a show's folder does — import, rescan, duplicates, rename,
// delete, Move to folder — is what v0.3.1 did, a refusal, or (for a #56-shaped show) an
// import into the one folder its season's files are already in.

type rbFile struct {
	rel     string
	tracked [2]int // {0,0}: on disk, not tracked
	missing bool   // tracked but not on disk
}

func rbSetup(t *testing.T, files []rbFile) (*lifecycleHarness, string) {
	t.Helper()
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	for _, f := range files {
		p := filepath.Join(tv, filepath.FromSlash(f.rel))
		if !f.missing {
			sparseFile(t, p, 60<<20)
		}
		if f.tracked != [2]int{} {
			rvSet(t, h, f.tracked[0], f.tracked[1], p)
		}
	}
	return h, tv
}

func rbExists(t *testing.T, tv string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(tv, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is gone: %v\n    tree: %s", rel, err, rvTree(tv))
		}
	}
}

// rbImport imports a download of one episode and returns where it landed ("" = nowhere).
func rbImport(t *testing.T, h *lifecycleHarness, season, ep int) string {
	t.Helper()
	name := "Show.S" + pad2(season) + "E" + pad2(ep) + ".1080p.WEB-DL-GRP"
	mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, h.seriesID, season)
	mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-15', 1)`, h.seriesID, season, ep)
	dl := h.download(t, name, name+".mkv")
	s, _ := h.c.series.Get(h.ctx, h.seriesID)
	if err := h.c.placementCheck(h.ctx, s, name); err != nil {
		return ""
	}
	h.c.importSeriesInto(h.ctx, s, dl, forceAll)
	return h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, season, ep).Path
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// rbFolderActionsRefused asserts that rescan (changing no records), duplicates, rename and
// review mapping all refuse a #56-shaped show.
func rbFolderActionsRefused(t *testing.T, h *lifecycleHarness) {
	t.Helper()
	before := zzRecords(t, h)
	var refused *RescanRefusedError
	if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.As(err, &refused) || !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("rescan = %v, want refused for an unclear folder", err)
	}
	if after := zzRecords(t, h); after != before {
		t.Errorf("rescan changed records:\n  before %s\n  after  %s", before, after)
	}
	if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("duplicates = %v, want refused", err)
	}
	if _, err := h.c.SeriesRenamePreview(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("rename preview = %v, want refused", err)
	}
	if _, err := h.c.SeriesRename(h.ctx, h.seriesID, nil); !errors.Is(err, series.ErrNoShowFolder) {
		t.Errorf("rename = %v, want refused", err)
	}
	if why := h.c.series.FolderUnclear(h.ctx, h.seriesID); why == "" {
		t.Error("FolderUnclear is empty: Move to folder would move it")
	}
}

func zzRecords(t *testing.T, h *lifecycleHarness) string {
	t.Helper()
	rows, err := h.c.db.Query(`SELECT series_id, season_number, episode_number, has_file, file_path FROM episodes ORDER BY series_id, season_number, episode_number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid int64
		var s, e, hf int
		var p string
		_ = rows.Scan(&sid, &s, &e, &hf, &p)
		out = append(out, strings.Join([]string{string(rune('0' + sid)), pad2(s), pad2(e), string(rune('0' + hf)), p}, ":"))
	}
	return strings.Join(out, " ; ")
}

// j, k, v, w, x: a season-named folder (S01) inside a folder that also holds another show —
// beside it, hidden, under an "Other" folder, or deep. The show's folder is never the
// folder above S01: a new episode of season 1 goes into S01 itself (it holds only the
// show's files), a new season waits for Review, every action on the whole folder is
// refused, and the other show is never touched.
func TestRevS01InsideAFolderOfShows(t *testing.T) {
	deep := "Star Trek/a/b/c/d/e/f/g/h/i/TNG/Season 1/TNG - S01E01.mkv"
	for _, c := range []struct {
		name, tracked string
		other         []string
	}{
		{"j-category", "Kids/S01/Show - S01E01.mkv", []string{"Kids/Bluey/Season 1/Bluey - S01E01 - 2160p.mkv"}},
		{"k-franchise", "Star Trek/S01/Show - S01E01.mkv", []string{"Star Trek/Star Trek The Next Generation/Season 1/TNG - S01E01 - 2160p.mkv"}},
		{"v-hidden", "Star Trek/S01/Show - S01E01.mkv", []string{"Star Trek/.TNG/Season 1/TNG - S01E01.mkv"}},
		{"w-under-Other", "Star Trek/S01/Show - S01E01.mkv", []string{"Star Trek/Other/TNG/Season 1/TNG - S01E01.mkv"}},
		{"x-deep", "Star Trek/S01/Show - S01E01.mkv", []string{deep}},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := []rbFile{{rel: c.tracked, tracked: [2]int{1, 1}}}
			for _, o := range c.other {
				files = append(files, rbFile{rel: o})
			}
			s01 := filepath.Dir(filepath.FromSlash(c.tracked))
			parent := filepath.Dir(s01)

			h, tv := rbSetup(t, files)
			if got := rbImport(t, h, 1, 3); filepath.Dir(got) != filepath.Join(tv, s01) {
				t.Errorf("S01E03 landed at %q, want in %s", got, s01)
			}
			noDir(t, filepath.Join(tv, parent, "Season 1"), "a season folder was made in the folder above")
			rbExists(t, tv, c.other...)

			h, tv = rbSetup(t, files)
			if got := rbImport(t, h, 2, 1); got != "" {
				t.Errorf("a new season landed at %s, want Review", got)
			}
			rbFolderActionsRefused(t, h)
			if p := h.c.series.FolderPath(h.ctx, h.seriesID); p != filepath.Join(tv, s01) {
				t.Errorf("FolderPath = %q, want the file's own folder", p)
			}
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, c.other...)
			if _, err := os.Stat(filepath.Join(tv, parent)); err != nil {
				t.Errorf("the folder above was removed: %v", err)
			}
		})
	}
}

// y: the season folder holds another show's file next to the show's own: nothing imports
// into it.
func TestRevSeasonFolderHoldingAnotherShowsFile(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Kids/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Kids/S01/Bluey - S01E01.mkv"},
	})
	if got := rbImport(t, h, 1, 3); got != "" {
		t.Errorf("S01E03 landed at %s, want Review", got)
	}
	rbFolderActionsRefused(t, h)
	if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
		t.Logf("delete: %v", err)
	}
	rbExists(t, tv, "Kids/S01/Bluey - S01E01.mkv")
}

// The Pitt, ongoing: S01E01-E04 recorded in The Pitt/S01. S01E05 joins them there; S02E01
// waits for Review — no folder is made.
func TestRevOngoing56Show(t *testing.T) {
	var files []rbFile
	for e := 1; e <= 4; e++ {
		files = append(files, rbFile{rel: "The Pitt/S01/The Pitt - S01E0" + string(rune('0'+e)) + ".mkv", tracked: [2]int{1, e}})
	}
	h, tv := rbSetup(t, files)
	if got := rbImport(t, h, 1, 5); filepath.Dir(got) != filepath.Join(tv, "The Pitt", "S01") {
		t.Errorf("S01E05 landed at %q", got)
	}
	if got := rbImport(t, h, 2, 1); got != "" {
		t.Errorf("S02E01 landed at %s, want Review", got)
	}
	noDir(t, filepath.Join(tv, "The Pitt", "Season 2"), "a season folder was made")
	noDir(t, filepath.Join(tv, "S01"), "the old #56 folder was made")
}

// p: the show's only recorded file was a #56 leftover the owner has since moved back to the
// show's real folder. Nothing imports, and no new folder is made.
func TestRevBAllLeftoversGone(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "S01/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "Real Folder/S01/Show - S01E01.mkv"},
	})
	if got := rbImport(t, h, 1, 2); got != "" {
		t.Errorf("S01E02 landed at %s, want Review", got)
	}
	rbFolderActionsRefused(t, h)
	noDir(t, filepath.Join(tv, "Show"), "a new folder was made for a show that has one")
	rbExists(t, tv, "Real Folder/S01/Show - S01E01.mkv")
}

// o: an existing "Season.01" folder is joined, not doubled by a "Season 1" beside it (the
// one place an ordinary show's import differs from v0.3.1's).
func TestRevBJoinsSeasonDot01(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{{rel: "Show/Season.01/Show - S01E01.mkv", tracked: [2]int{1, 1}}})
	if got := rbImport(t, h, 1, 2); filepath.Dir(got) != filepath.Join(tv, "Show", "Season.01") {
		t.Errorf("S01E02 at %q, want in Show/Season.01", got)
	}
	noDir(t, filepath.Join(tv, "Show", "Season 1"), "a parallel Season 1 was made")
}

// r: a bonus folder named like a season ("Series 1 (Behind the scenes)") is never joined by
// an import: as v0.3.1, season 1 goes into "Season 1".
func TestRevBNeverJoinsATaggedSeasonFolder(t *testing.T) {
	h, tv := rbSetup(t, []rbFile{
		{rel: "Show/Season 2/Show - S02E01.mkv", tracked: [2]int{2, 1}},
		{rel: "Show/Series 1 (Behind the scenes)/bonus.mkv"},
	})
	if got := rbImport(t, h, 1, 2); filepath.Dir(got) != filepath.Join(tv, "Show", "Season 1") {
		t.Errorf("S01E02 at %q, want in Show/Season 1", got)
	}
}
