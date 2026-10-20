package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// pittMeta is TMDB for one show, The Pitt (id 7), found only by its own name.
type pittMeta struct{}

func (pittMeta) Available() bool { return true }
func (pittMeta) SearchSeries(_ context.Context, q string) ([]metadata.SeriesResult, error) {
	if parser.TitleKey(q) != parser.TitleKey("The Pitt") {
		return nil, nil
	}
	return []metadata.SeriesResult{{TMDBID: 7, Title: "The Pitt", Year: 2025}}, nil
}
func (pittMeta) GetSeries(context.Context, int) (*metadata.SeriesDetails, error) {
	var eps []metadata.EpisodeDetails
	for e := 1; e <= 10; e++ {
		eps = append(eps, metadata.EpisodeDetails{EpisodeNumber: e, AirDate: "2025-01-09"})
	}
	return &metadata.SeriesDetails{
		SeriesResult:    metadata.SeriesResult{TMDBID: 7, Title: "The Pitt", Year: 2025},
		NumberingSource: "tmdb",
		Seasons:         []metadata.SeasonDetails{{SeasonNumber: 1, Episodes: eps}, {SeasonNumber: 2, Episodes: eps}},
	}, nil
}

// pittHarness is a lifecycle harness whose TV folder holds The Pitt, tracked, its first
// four episodes in <TV>/<under>/S01 — the #56 shape. Temp dirs only.
func pittHarness(t *testing.T, under string) (*lifecycleHarness, string, int64) {
	t.Helper()
	h := newLifecycleHarness(t)
	tv := t.TempDir()
	h.c.series = series.NewService(h.c.db, pittMeta{}, tv, h.c.log)
	h.c.imp = library.NewImporter(tv, h.c.log)
	s, err := h.c.series.Add(h.ctx, 7, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 4; ep++ {
		p := filepath.Join(tv, filepath.FromSlash(under), "S01", "The Pitt - S01E0"+string(rune('0'+ep))+".mkv")
		setEpisodeFile(t, h.c, s.ID, 1, ep, libFile(t, p), "The.Pitt.S01.1080p.WEB-DL-GRP")
	}
	return h, tv, s.ID
}

// pittImport imports one episode download for The Pitt and returns where it landed ("" if
// it didn't).
func pittImport(t *testing.T, h *lifecycleHarness, id int64, season, ep int) string {
	t.Helper()
	name := "The.Pitt.S0" + string(rune('0'+season)) + "E0" + string(rune('0'+ep)) + ".1080p.WEB-DL-GRP"
	dl := h.download(t, name, name+".mkv")
	s, err := h.c.series.Get(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.c.placementCheck(h.ctx, s, name); err != nil {
		return ""
	}
	h.c.importSeriesInto(h.ctx, s, dl, forceAll)
	return h.c.series.CurrentEpisodeFile(h.ctx, id, season, ep).Path
}

// The way out the changelog gives for a #56-shaped show, walked literally: delete the show
// in Arrmada keeping its files, rename its season folder to "Season 01", scan the library.
// The scan adds it back with its files, and the next episode lands in Season 01.
func TestWayOutDeleteRenameScan(t *testing.T) {
	h, tv, id := pittHarness(t, "The Pitt")
	own := filepath.Join(tv, "The Pitt")

	// As it is: an episode of season 1 goes next to the others; season 2 waits.
	e5 := pittImport(t, h, id, 1, 5)
	if filepath.Dir(e5) != filepath.Join(own, "S01") {
		t.Fatalf("S01E05 landed at %q, want in The Pitt/S01", e5)
	}
	if got := pittImport(t, h, id, 2, 1); got != "" {
		t.Fatalf("S02E01 landed at %q, want Needs you", got)
	}
	var refused *RescanRefusedError
	if err := h.c.RescanSeries(h.ctx, id); !errors.As(err, &refused) {
		t.Fatalf("rescan = %v, want refused", err)
	}

	// 1. Delete the show, keeping its files.
	if _, err := h.c.series.Delete(h.ctx, id, false); err != nil {
		t.Fatal(err)
	}
	for ep := 1; ep <= 4; ep++ {
		if _, err := os.Stat(filepath.Join(own, "S01", "The Pitt - S01E0"+string(rune('0'+ep))+".mkv")); err != nil {
			t.Fatalf("a file went with the show: %v", err)
		}
	}
	if _, err := os.Stat(e5); err != nil {
		t.Fatalf("S01E05's file went with the show: %v", err)
	}
	// 2. Rename the season folder.
	if err := os.Rename(filepath.Join(own, "S01"), filepath.Join(own, "Season 01")); err != nil {
		t.Fatal(err)
	}
	// 3. Scan the library.
	res, err := h.c.series.ScanLibrary(h.ctx, "", nil)
	if err != nil || res.Imported != 1 {
		t.Fatalf("scan = %+v, %v; want The Pitt added", res, err)
	}
	s, err := h.c.series.GetByTMDB(h.ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if s.Monitored {
		t.Error("the show came back with searching on; the changelog says off")
	}
	for ep := 1; ep <= 5; ep++ {
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, s.ID, 1, ep); filepath.Dir(cur.Path) != filepath.Join(own, "Season 01") {
			t.Errorf("after the scan S01E%02d = %q", ep, cur.Path)
		}
	}
	if info := h.c.series.ShowFolderOf(h.ctx, s.ID); info.Unknown || info.Path != own {
		t.Fatalf("ShowFolderOf = %+v, want %s", info, own)
	}
	// 4. The next episode lands in Season 01, a new season beside it, and rescan works.
	if got := pittImport(t, h, s.ID, 1, 6); filepath.Dir(got) != filepath.Join(own, "Season 01") {
		t.Errorf("S01E06 landed at %q, want in The Pitt/Season 01", got)
	}
	if got := pittImport(t, h, s.ID, 2, 1); filepath.Dir(filepath.Dir(got)) != own {
		t.Errorf("S02E01 landed at %q, want a season folder in The Pitt", got)
	}
	if err := h.c.RescanSeries(h.ctx, s.ID); err != nil {
		t.Errorf("rescan after the way out: %v", err)
	}
}

// The same way out for a show inside a category folder (<TV>/Kids/The Pitt/S01) doesn't
// work: the library scan reads only the folders right inside the TV folder, so it sees
// "Kids", not The Pitt, and the show isn't added back (its files are untouched). Even if it
// were added back, v0.3.1's rule joins the show folder's name onto the TV folder, so new
// episodes would go to a new <TV>/The Pitt beside the category. Renaming without deleting
// changes nothing: the records still point at S01 and the show stays refused. Such a show
// is best kept as it is: new episodes of its seasons still import next to the others.
func TestWayOutInsideACategory(t *testing.T) {
	t.Run("delete, rename, scan: not added back", func(t *testing.T) {
		h, tv, id := pittHarness(t, "Kids/The Pitt")
		own := filepath.Join(tv, "Kids", "The Pitt")
		if _, err := h.c.series.Delete(h.ctx, id, false); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(own, "S01"), filepath.Join(own, "Season 01")); err != nil {
			t.Fatal(err)
		}
		res, err := h.c.series.ScanLibrary(h.ctx, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Imported != 0 {
			t.Errorf("scan added %d shows from the category folder", res.Imported)
		}
		if _, err := h.c.series.GetByTMDB(h.ctx, 7); err == nil {
			t.Error("The Pitt was added back from inside the category")
		}
		rbExists(t, tv, "Kids/The Pitt/Season 01/The Pitt - S01E01.mkv", "Kids/The Pitt/Season 01/The Pitt - S01E04.mkv")
	})
	t.Run("rename only: still refused, nothing re-pointed", func(t *testing.T) {
		h, tv, id := pittHarness(t, "Kids/The Pitt")
		own := filepath.Join(tv, "Kids", "The Pitt")
		if err := os.Rename(filepath.Join(own, "S01"), filepath.Join(own, "Season 01")); err != nil {
			t.Fatal(err)
		}
		// Every record still points at S01, which is gone: the show is still #56-shaped and
		// refused — nothing re-points the records.
		var refused *RescanRefusedError
		if err := h.c.RescanSeries(h.ctx, id); !errors.As(err, &refused) {
			t.Fatalf("rescan = %v, want refused", err)
		}
		if cur := h.c.series.CurrentEpisodeFile(h.ctx, id, 1, 1); cur.Path != filepath.Join(own, "S01", "The Pitt - S01E01.mkv") {
			t.Errorf("S01E01 = %q, want unchanged", cur.Path)
		}
		if got := pittImport(t, h, id, 1, 5); got != "" {
			t.Errorf("S01E05 landed at %q, want Needs you (its season folder is gone)", got)
		}
	})
	t.Run("kept as it is", func(t *testing.T) {
		h, tv, id := pittHarness(t, "Kids/The Pitt")
		if got := pittImport(t, h, id, 1, 5); filepath.Dir(got) != filepath.Join(tv, "Kids", "The Pitt", "S01") {
			t.Errorf("S01E05 landed at %q, want in Kids/The Pitt/S01", got)
		}
		if got := pittImport(t, h, id, 2, 1); got != "" {
			t.Errorf("S02E01 landed at %q, want Needs you", got)
		}
		noDir(t, filepath.Join(tv, "The Pitt"), "a folder was made beside the category")
		noDir(t, filepath.Join(tv, "Kids", "Season 1"), "a season folder was made in the category")
	})
}
