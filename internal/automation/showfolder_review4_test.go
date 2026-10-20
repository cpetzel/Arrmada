package automation

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// Fourth review of the #56 hotfix: the show's folder is v0.3.1's answer from the FIRST
// recorded file, with nothing substituted for it, and nothing ever re-points a record. For
// each layout, either the show reads exactly as v0.3.1 read it, or rescan, duplicates and
// delete leave every other file and every record alone. Temp dirs only.

type r4Layout struct {
	name  string
	files []rbFile
	other []rbFile // tracked files of a second show, "Other"
	keep  []string // files that are not the show's: never touched
}

var r4Layouts = []r4Layout{
	{name: "G3 first record gone, franchise", files: []rbFile{
		{rel: "Star Trek/S02/Show - S02E01.mkv", tracked: [2]int{2, 1}, missing: true},
		{rel: "Star Trek/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"},
	}, keep: []string{"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"}},
	{name: "G4 first record gone, its folder kept", files: []rbFile{
		{rel: "Star Trek/S02/Show - S02E01.mkv", tracked: [2]int{2, 1}, missing: true},
		{rel: "Star Trek/S02/folder.jpg"},
		{rel: "Star Trek/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
		{rel: "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"},
	}, keep: []string{"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"}},
	{name: "F1 another tracked show's Season 1 beside", files: []rbFile{
		{rel: "Shared/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "Shared/S01/Show - S01E02.mkv", tracked: [2]int{1, 2}},
	}, other: []rbFile{{rel: "Shared/Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}}},
		keep: []string{"Shared/Season 1/Show - S01E01.mkv"}},
	{name: "F2 generic names, category Season 1 beside", files: []rbFile{
		{rel: "Kids/S01/01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "Kids/Season 1/01.mkv"},
		{rel: "Kids/Bluey/Season 1/Bluey - S01E01.mkv"},
	}, keep: []string{"Kids/Season 1/01.mkv", "Kids/Bluey/Season 1/Bluey - S01E01.mkv"}},
	{name: "W2 renamed inside a franchise", files: []rbFile{
		{rel: "Star Trek/S01/Show - S01E01.mkv", tracked: [2]int{1, 1}, missing: true},
		{rel: "Star Trek/Season 01/Show - S01E01.mkv"},
		{rel: "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E01 - 2160p.mkv"},
		{rel: "Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv"},
	}, keep: []string{
		"Star Trek/Season 01/Show - S01E01.mkv",
		"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E01 - 2160p.mkv",
		"Star Trek/Star Trek The Next Generation/Season 1/Star Trek The Next Generation - S01E02 - 2160p.mkv",
	}},
	{name: "D1 season folder right in the TV folder", files: []rbFile{
		{rel: "Season 1/Show - S01E01.mkv", tracked: [2]int{1, 1}},
	}},
}

func r4Setup(t *testing.T, l r4Layout) (*lifecycleHarness, string) {
	t.Helper()
	h, tv := rbSetup(t, l.files)
	if len(l.other) > 0 {
		res, err := h.c.db.Exec(`INSERT INTO series (tmdb_id, title, monitored) VALUES (99, 'Other', 1)`)
		if err != nil {
			t.Fatal(err)
		}
		oid, _ := res.LastInsertId()
		for _, f := range l.other {
			p := filepath.Join(tv, filepath.FromSlash(f.rel))
			sparseFile(t, p, 60<<20)
			mustExec(t, h.c, `INSERT OR IGNORE INTO seasons (series_id, season_number, monitored) VALUES (?, ?, 1)`, oid, f.tracked[0])
			mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-01', 1)`, oid, f.tracked[0], f.tracked[1])
			mustExec(t, h.c, `UPDATE episodes SET has_file = 1, file_path = ? WHERE series_id = ? AND season_number = ? AND episode_number = ?`, p, oid, f.tracked[0], f.tracked[1])
		}
	}
	return h, tv
}

// r4Records is every episode record, every show's.
func r4Records(t *testing.T, h *lifecycleHarness) string {
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
		if err := rows.Scan(&sid, &s, &e, &hf, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d:S%02dE%02d:%d:%s", sid, s, e, hf, p))
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// r4First is the file v0.3.1 read the show's folder from: the same query.
func r4First(t *testing.T, h *lifecycleHarness) string {
	t.Helper()
	var p string
	if err := h.c.db.QueryRow(`SELECT file_path FROM episodes WHERE series_id = ? AND has_file = 1 AND file_path <> '' LIMIT 1`, h.seriesID).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRevDFolderIsV031sFirstFileAndNothingIsRepointed(t *testing.T) {
	for _, l := range r4Layouts {
		t.Run(l.name, func(t *testing.T) {
			h, tv := r4Setup(t, l)
			first := r4First(t, h)
			info := h.c.series.ShowFolderOf(h.ctx, h.seriesID)
			v031 := filepath.Dir(first)
			if b := filepath.Base(v031); strings.EqualFold(b, "specials") || strings.HasPrefix(strings.ToLower(b), "season") {
				v031 = filepath.Dir(v031)
			}
			if !info.Unknown && info.Path != v031 {
				t.Fatalf("ShowFolderOf = %+v, want v0.3.1's %s (from the first record %s)", info, v031, first)
			}
			if !info.Unknown {
				return // v0.3.1's own answer: v0.3.1's behaviour, nothing more to check here
			}
			before := r4Records(t, h)
			var refused *RescanRefusedError
			if err := h.c.RescanSeries(h.ctx, h.seriesID); !errors.As(err, &refused) {
				t.Errorf("rescan = %v, want refused", err)
			}
			if after := r4Records(t, h); after != before {
				t.Errorf("a refused rescan changed the records:\nbefore\n%s\nafter\n%s", before, after)
			}
			if _, err := h.c.SeriesDuplicates(h.ctx, h.seriesID); !errors.Is(err, series.ErrNoShowFolder) {
				t.Errorf("duplicates = %v, want refused", err)
			}
			if got := pittImportAs(t, h, "Show.S01E03.1080p.WEB-DL-GRP", 1, 3); got != "" && l.name[:2] == "D1" {
				t.Errorf("S01E03 landed at %s, want Needs you", got)
			}
			if _, err := h.c.series.Delete(h.ctx, h.seriesID, true); err != nil {
				t.Logf("delete: %v", err)
			}
			rbExists(t, tv, l.keep...)
			if l.other != nil {
				rbExists(t, tv, l.other[0].rel)
			}
		})
	}
}

// pittImportAs imports one episode download (release name) for the harness's show through
// the sweep's check and returns where it landed ("" when it waits in Needs you).
func pittImportAs(t *testing.T, h *lifecycleHarness, name string, season, ep int) string {
	t.Helper()
	mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, ?, ?, '2020-01-15', 1)`, h.seriesID, season, ep)
	dl := h.download(t, name, name+".mkv")
	s, err := h.c.series.Get(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.c.placementCheck(h.ctx, s, name); err != nil {
		return ""
	}
	h.c.importSeriesInto(h.ctx, s, dl, forceAll)
	return h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, season, ep).Path
}
