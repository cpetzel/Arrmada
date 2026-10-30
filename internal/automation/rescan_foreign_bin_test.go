package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file named as another show still fills an episode with no file in the show's proven
// "Title (Year)/Season N" folder (a localized or romaji name of the show's own episode
// depends on it). When an import later brings the episode's real file, the filled one goes
// to the recycle bin — never deleted for good — and the other episodes keep theirs.
func TestForeignFilledEpisodeIsRecycledWhenAnImportReplacesIt(t *testing.T) {
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	bin := t.TempDir()
	h.c.series.SetRecycleDir(bin)
	mustExec(t, h.c, `UPDATE series SET year = 2010 WHERE id = ?`, h.seriesID)

	season := filepath.Join(tv, "Show (2010)", "Season 1")
	e1 := filepath.Join(season, "Show - S01E01.mkv")
	foreign := filepath.Join(season, "Beta - 1x02 - Other.mkv")
	sparseFile(t, e1, 60<<20)
	sparseFile(t, foreign, 90<<20)
	rvSet(t, h, 1, 1, e1)

	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		t.Fatal(err)
	}
	if p := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2).Path; filepath.Clean(p) != filepath.Clean(foreign) {
		t.Fatalf("after the rescan E2 = %q, want the foreign-named file filling it", p)
	}

	name := "Show.S01E02.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	h.seriesGrab(t, name, hash)
	dir := h.download(t, name, name+".mkv")
	if err := h.c.ImportReview(h.ctx, h.hold(t, hash, name, dir), 0, ""); err != nil {
		t.Fatal(err)
	}

	now := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 2).Path
	if now == "" || filepath.Clean(now) == filepath.Clean(foreign) {
		t.Fatalf("after the import E2 = %q, want the imported file", now)
	}
	if _, err := os.Stat(now); err != nil {
		t.Fatalf("the imported file isn't there: %v", err)
	}
	if _, err := os.Stat(foreign); !os.IsNotExist(err) {
		t.Fatalf("the replaced file is still in the season folder (err %v)", err)
	}
	var binned []string
	_ = filepath.Walk(bin, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			binned = append(binned, filepath.Base(p))
		}
		return nil
	})
	found := false
	for _, b := range binned {
		found = found || strings.Contains(b, "Beta - 1x02 - Other")
	}
	if !found {
		t.Errorf("the replaced file isn't in the recycle bin: bin holds %q", binned)
	}
	if p := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, 1).Path; filepath.Clean(p) != filepath.Clean(e1) {
		t.Errorf("E1 = %q, want it untouched", p)
	}
	if _, err := os.Stat(e1); err != nil {
		t.Errorf("E1's file: %v", err)
	}
}
