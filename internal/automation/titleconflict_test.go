package automation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// The Nobody Wants This pack with the stale listing is held in Needs you as a numbering
// review whose reason names the files that disagree and both readings of each, nothing is
// placed or blocklisted, and mapping the files by hand (pre-filled from the filenames)
// imports every episode where its filename says.
func TestTitleConflictHeldThenMappedByHand(t *testing.T) {
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := seedTitledSeason(t, h, nwtShow(nwtStale))
	hash, _, _ := dropDownload(t, h, nwtPack, nwtFiles)

	h.c.ImportSeriesDownloads(h.ctx)
	h.c.ImportSeriesDownloads(h.ctx)

	reviews, err := h.c.ListReviews(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviews) != 1 || reviews[0].Hash != hash {
		t.Fatalf("reviews = %+v, want the pack held", reviews)
	}
	r := reviews[0]
	if r.ReasonCode != ReasonTitleConflict || r.ExpectedID != id {
		t.Errorf("held as %q for %d, want a title-conflict review for the show (it offers Map files…)", r.ReasonCode, r.ExpectedID)
	}
	for _, want := range []string{
		nwtFiles[3] + ` (numbered S03E04, titled "Brians Wedding")`,
		`S03E05 "Brian's Wedding"`,
		nwtFiles[4] + ` (numbered S03E05, titled "The Love Card")`,
		nwtFiles[6] + ` (numbered S03E07, titled "Watch What Happens")`,
		nwtFiles[7] + ` (numbered S03E08, titled "One in a Million")`,
		"map the files to episodes by hand",
	} {
		if !strings.Contains(r.Reason, want) {
			t.Errorf("reason doesn't say %q:\n%s", want, r.Reason)
		}
	}
	if got := filledEpisodes(t, h, id); got != 0 {
		t.Errorf("%d episodes have files, want none placed", got)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("blocklist = %d, want 0 — nothing is wrong with the release", n)
	}

	// "Import anyway" (Review's import into the show) refuses: it would misplace a file.
	if err := h.c.ImportReview(h.ctx, r.ID, id, "series"); !errors.Is(err, ErrNothingToImport) || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("import into the show = %v, want the disagreement", err)
	}
	if got := filledEpisodes(t, h, id); got != 0 {
		t.Fatalf("%d episodes have files after the refused import", got)
	}

	// Map files…, as the modal pre-fills it: each file by its own number.
	var maps []FileMapping
	for i, f := range nwtFiles {
		maps = append(maps, FileMapping{RelPath: f, Season: 3, Episodes: []int{i + 1}})
	}
	if n, err := h.c.ImportReviewMapped(h.ctx, r.ID, id, maps); err != nil || n != 10 {
		t.Fatalf("mapped import = %d, %v; want 10 episodes", n, err)
	}
	if got := describeSeason(t, h, id, 3, hash, nwtFiles); got != "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10" {
		t.Errorf("after mapping: %s", got)
	}
	if left, _ := h.c.ListReviews(h.ctx); len(left) != 0 {
		t.Errorf("review still pending after mapping: %+v", left)
	}
}

// A manual folder import of the same pack refuses too, saying why.
func TestTitleConflictManualFolderImportRefuses(t *testing.T) {
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := seedTitledSeason(t, h, nwtShow(nwtStale))
	_, _, dir := dropDownload(t, h, nwtPack, nwtFiles)
	err := h.c.ManualImportSeries(h.ctx, id, dir)
	if err == nil || !strings.Contains(err.Error(), "Brians Wedding") {
		t.Fatalf("manual import = %v, want the disagreement", err)
	}
	if got := filledEpisodes(t, h, id); got != 0 {
		t.Fatalf("%d episodes have files", got)
	}
}

// The CAKES single, E07 with no title, imports alone onto E07 while the stale listing calls
// E07 "May I Have This Dance" — a number with no title is placed by the number, as before.
func TestSingleEpisodeNoTitleImportsByNumber(t *testing.T) {
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := seedTitledSeason(t, h, nwtShow(nwtStale))
	files := []string{"Nobody.Wants.This.S03E07.1080p.WEB.h264-CAKES.mkv"}
	hash, _, _ := dropDownload(t, h, "", files)
	h.c.ImportSeriesDownloads(h.ctx)
	if got := describeSeason(t, h, id, 3, hash, files); got != "1:- 2:- 3:- 4:- 5:- 6:- 7:7 8:- 9:- 10:-" {
		t.Errorf("got %s, want E07 on episode 7", got)
	}
}

// A release already recorded as imported — and blocklisted by the re-process that found
// nothing new to place — is imported again when the user grabs it again by hand, with the
// quality gate skipped inside the scope it was picked for.
func TestManualRegrabOfImportedReleaseImportsAgain(t *testing.T) {
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := seedTitledSeason(t, h, nwtShow(nwtFixed))
	hash, name, _ := dropDownload(t, h, nwtPack, nwtFiles)
	// What the owner's library held: the pack recorded as imported, and blocklisted as
	// "fully imported" by the next sweep.
	h.c.recordImportedHash(h.ctx, hash, name, 0)
	h.c.addBlockSeries(h.ctx, id, name, "Fake", incompleteSeasonReason(0))

	h.c.ImportSeriesDownloads(h.ctx)
	if got := filledEpisodes(t, h, id); got != 0 {
		t.Fatalf("premise: the recorded, blocklisted pack is passed over (got %d episodes)", got)
	}

	if err := h.c.GrabForSeries(h.ctx, id, "Fake", magnetFor(name), name, GrabScope{Season: 3}); err != nil {
		t.Fatal(err)
	}
	if force := h.c.grabForce(h.ctx, hash); !force.On {
		t.Fatal("the re-grab isn't recorded as the user's")
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if got := describeSeason(t, h, id, 3, hash, nwtFiles); got != "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10" {
		t.Errorf("after the re-grab: %s", got)
	}
	if !h.c.hashAlreadyImported(h.ctx, hash) {
		t.Error("the re-grab's import isn't recorded")
	}
}

// An automatic grab of an imported release changes nothing: only the user's say-so reopens it.
func TestAutoRegrabDoesNotReopen(t *testing.T) {
	h := newStallHarness(t)
	id := seedTitledSeason(t, h, nwtShow(nwtFixed))
	h.c.recordImportedHash(h.ctx, hashFor(nwtPack), nwtPack, 0)
	h.c.addBlockSeries(h.ctx, id, nwtPack, "Fake", incompleteSeasonReason(0))
	if err := h.c.GrabForSeriesAuto(h.ctx, id, "Fake", magnetFor(nwtPack), nwtPack); err != nil {
		t.Fatal(err)
	}
	if !h.c.hashAlreadyImported(h.ctx, hashFor(nwtPack)) || h.blocklistCount(t) != 1 {
		t.Error("an automatic grab reopened an imported release")
	}
}

// Unit cases for the rule itself, independent of the store.
func TestTitleConflictRule(t *testing.T) {
	ref := func(e int) []series.EpisodeRef { return []series.EpisodeRef{{Season: 1, Episode: e}} }
	file := func(name string, numbered, placed []series.EpisodeRef) resolvedVideo {
		return resolvedVideo{v: library.FoundVideo{Path: "/dl/" + name}, numbered: numbered, refs: placed, byTitle: !sameRefs(numbered, placed)}
	}
	two := []series.EpisodeRef{{Season: 1, Episode: 1}, {Season: 1, Episode: 2}}
	h := newStallHarness(t)
	s := series.Series{ID: 1, Title: "Show"}
	for _, c := range []struct {
		name  string
		files []resolvedVideo
		clash bool
	}{
		{"moved onto a sibling's number", []resolvedVideo{file("a", ref(4), ref(5)), file("b", ref(5), ref(5))}, true},
		{"two moved onto one episode", []resolvedVideo{file("a", ref(4), ref(5)), file("b", ref(6), ref(5))}, true},
		{"moved onto the first half of a two-parter", []resolvedVideo{file("a", two, two), file("b", ref(3), ref(1))}, true},
		{"moved beside a two-parter's second half", []resolvedVideo{file("a", two, two), file("b", ref(3), ref(2))}, false},
		{"whole pack shifted", []resolvedVideo{file("a", ref(2), ref(1)), file("b", ref(3), ref(2))}, false},
		{"two copies by number", []resolvedVideo{file("a", ref(1), ref(1)), file("b", ref(1), ref(1))}, false},
		{"one file moved alone", []resolvedVideo{file("a", ref(4), ref(5))}, false},
		{"two copies moved together", []resolvedVideo{file("a", ref(4), ref(3)), file("a.REPACK", ref(4), ref(3))}, false},
		{"two copies moved together onto a sibling's number", []resolvedVideo{file("a", ref(4), ref(3)), file("a.REPACK", ref(4), ref(3)), file("b", ref(3), ref(3))}, true},
		{"a copy of the sibling, and a mover", []resolvedVideo{file("b", ref(3), ref(3)), file("a", ref(4), ref(3)), file("b.PROPER", ref(3), ref(3))}, true},
	} {
		got := h.c.titleConflict(h.ctx, s, c.files) != ""
		if got != c.clash {
			t.Errorf("%s: conflict=%v, want %v", c.name, got, c.clash)
		}
	}
}

// filledEpisodes counts the show's episodes that have a file.
func filledEpisodes(t *testing.T, h *stallHarness, id int64) int {
	t.Helper()
	s, err := h.c.series.Get(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, sn := range s.Seasons {
		for _, ep := range sn.Episodes {
			if ep.HasFile {
				n++
			}
		}
	}
	return n
}

// newTitleHarness is the stall harness with an importer and a show whose library listing is
// sh, the metadata source agreeing with it.
func newTitleHarness(t *testing.T, sh layoutShow) (*stallHarness, int64) {
	t.Helper()
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, seedTitledSeason(t, h, sh)
}

// When the metadata source already has the corrected listing, the disagreement refreshes
// the show once and the pack imports by its numbers, with nothing held.
func TestTitleConflictRefreshSettlesIt(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	meta := serveListing(t, h, nwtShow(nwtFixed))
	hash, _, _ := dropDownload(t, h, nwtPack, nwtFiles)
	h.c.ImportSeriesDownloads(h.ctx)
	if reviews, _ := h.c.ListReviews(h.ctx); len(reviews) != 0 {
		t.Fatalf("held after the refresh settled it: %+v", reviews)
	}
	if got := describeSeason(t, h, id, 3, hash, nwtFiles); got != "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10" {
		t.Errorf("after the refresh: %s", got)
	}
	if n := meta.asked(); n != 1 {
		t.Errorf("metadata asked %d times, want once", n)
	}
}

// When the source still has the stale listing the pack is held after the one refresh, and a
// download refreshes its show at most once an hour.
func TestTitleConflictPersistsAfterRefreshIsHeld(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	meta := serveListing(t, h, nwtShow(nwtStale))
	hash, name, _ := dropDownload(t, h, nwtPack, nwtFiles)
	h.c.ImportSeriesDownloads(h.ctx)
	reviews, _ := h.c.ListReviews(h.ctx)
	if len(reviews) != 1 || reviews[0].ReasonCode != ReasonTitleConflict {
		t.Fatalf("reviews = %+v, want the pack held for its titles", reviews)
	}
	if got := filledEpisodes(t, h, id); got != 0 {
		t.Errorf("%d episodes have files", got)
	}
	if n := meta.asked(); n != 1 {
		t.Fatalf("metadata asked %d times, want once", n)
	}
	s, _ := h.c.series.Get(h.ctx, id)
	if h.c.refreshForTitleConflict(h.ctx, s, hash, name) {
		t.Error("refreshed again within the hour")
	}
	h.later(61 * time.Minute)
	if !h.c.refreshForTitleConflict(h.ctx, s, hash, name) || meta.asked() != 2 {
		t.Errorf("no refresh an hour later (asked %d)", meta.asked())
	}
}

// A held download the user dismissed stays dismissed: the sweep's re-process of a pack
// whose season still has gaps leaves it alone — for a title conflict, and for a pack none
// of whose files could be numbered.
func TestDismissedHoldStaysDismissed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		code  string
	}{
		{"titles and numbers disagree", nwtFiles, ReasonTitleConflict},
		{"no file could be numbered", []string{"nwt-bonus-one.mkv", "nwt-bonus-two.mkv"}, ReasonNumbering},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTitleHarness(t, nwtShow(nwtStale))
			dropDownload(t, h, nwtPack, tc.files)
			h.c.ImportSeriesDownloads(h.ctx)
			reviews, _ := h.c.ListReviews(h.ctx)
			if len(reviews) != 1 || reviews[0].ReasonCode != tc.code {
				t.Fatalf("premise: reviews = %+v, want one %s hold", reviews, tc.code)
			}
			if err := h.c.DismissReview(h.ctx, reviews[0].ID); err != nil {
				t.Fatal(err)
			}
			h.c.ImportSeriesDownloads(h.ctx)
			h.c.ImportSeriesDownloads(h.ctx)
			if again, _ := h.c.ListReviews(h.ctx); len(again) != 0 {
				t.Errorf("held again after it was dismissed: %+v", again)
			}
		})
	}
}

// A pack imported once, grabbed again by hand, held and then dismissed keeps its files when
// its seed goal is met: the first grab's 'imported' no longer speaks for the torrent.
func TestRegrabHeldDismissedKeepsFilesAtSeedGoal(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	hash, name, _ := dropDownload(t, h, nwtPack, nwtFiles)
	// The first import: grabbed, imported, recorded, then blocklisted by the re-process.
	h.c.recordSeriesGrab(h.ctx, id, name, "Fake", "", hash)
	h.c.markSeriesGrabImported(h.ctx, id, hash, name)
	h.c.recordImportedHash(h.ctx, hash, name, 0)
	h.c.addBlockSeries(h.ctx, id, name, "Fake", incompleteSeasonReason(0))

	if err := h.c.GrabForSeries(h.ctx, id, "Fake", magnetFor(name), name, GrabScope{Season: 3}); err != nil {
		t.Fatal(err)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	reviews, _ := h.c.ListReviews(h.ctx)
	if len(reviews) != 1 {
		t.Fatalf("premise: the re-grab is held (reviews %+v)", reviews)
	}
	if err := h.c.DismissReview(h.ctx, reviews[0].ID); err != nil {
		t.Fatal(err)
	}

	// Its seed goal met.
	h.qbit.mu.Lock()
	for _, tor := range h.qbit.torrents {
		if tor["hash"] == hash {
			tor["completed"], tor["downloaded"], tor["uploaded"], tor["seeding_time"] = 1<<30, 1<<30, 50<<30, 4000*3600
		}
	}
	h.qbit.mu.Unlock()
	h.c.downloads.InvalidateSnapshot()
	var removed []removeCall
	h.c.removeTorrent = func(_ context.Context, hash string, deleteData bool) error {
		removed = append(removed, removeCall{hash, deleteData})
		return nil
	}
	h.c.ManageSeeding(h.ctx)
	if len(removed) == 0 {
		t.Fatal("premise: seed cleanup removed nothing — the goal isn't met")
	}
	for _, r := range removed {
		if r.deleteData {
			t.Fatalf("the torrent was removed with its data: %+v", removed)
		}
	}
	var superseded int
	if err := h.c.db.QueryRow(`SELECT COUNT(*) FROM grabs WHERE status = ?`, grabStatusSuperseded).Scan(&superseded); err != nil {
		t.Fatal(err)
	}
	if superseded != 1 {
		t.Errorf("superseded grabs = %d, want the first one", superseded)
	}
}

// Seed cleanup goes by the newest grab of a torrent: an earlier 'imported' grab of the same
// hash doesn't decide that a later, dismissed one's files are deleted.
func TestSeedMatcherPrefersTheNewestGrab(t *testing.T) {
	hash := hashFor(nwtPack)
	grabs := []grab{
		{ID: 1, InfoHash: hash, Status: grabStatusImported},
		{ID: 2, InfoHash: hash, Status: grabStatusDismissed},
	}
	it := download.Item{Hash: hash, Name: nwtPack}
	m := newSeedMatcher(grabs, []download.Item{it})
	if g := m.match(it); g == nil || g.ID != 2 {
		t.Fatalf("matched %+v, want the newer grab", g)
	}
}

// A re-grab reopens only a show's own import record: a film imported from the same torrent
// keeps its record.
func TestRegrabLeavesAFilmsImportAlone(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtFixed))
	if _, err := h.c.db.Exec(`INSERT INTO imports (download_hash, source_path, target_path, title, size_bytes, movie_id)
		VALUES (?, '/dl/film.mkv', '/movies/Film (2020)/Film (2020).mkv', ?, 0, 5)`, hashFor(nwtPack), nwtPack); err != nil {
		t.Fatal(err)
	}
	if err := h.c.GrabForSeries(h.ctx, id, "Fake", magnetFor(nwtPack), nwtPack, GrabScope{Season: 3}); err != nil {
		t.Fatal(err)
	}
	if !h.c.hashAlreadyImported(h.ctx, hashFor(nwtPack)) {
		t.Error("a film's import record was deleted by a show's re-grab")
	}
}
