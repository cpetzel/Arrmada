package automation

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/series"
)

// newestGrabUnmatched is the import_unmatched mark on the newest grab of hash.
func newestGrabUnmatched(t *testing.T, h *stallHarness, hash string) int {
	t.Helper()
	var n int
	if err := h.c.db.QueryRow(`SELECT import_unmatched FROM grabs WHERE lower(info_hash) = lower(?) ORDER BY id DESC LIMIT 1`, hash).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// meetSeedGoal makes hash's torrent in the fake client meet any seed goal, runs seed cleanup
// and answers what it removed.
func meetSeedGoal(t *testing.T, h *stallHarness, hash string) []removeCall {
	t.Helper()
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
	return removed
}

// An earlier import left two files unmatched, so its torrent kept seeding. Grabbed again by
// hand, held, and mapped by hand for eight of its ten files, it must still keep seeding at
// its goal: the re-grab carries the mark, and the mapping notes the files it left out.
func TestRegrabMappedInPartKeepsUnmatchedFiles(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	hash, name, _ := dropDownload(t, h, nwtPack, nwtFiles)
	h.c.recordSeriesGrab(h.ctx, id, name, "Fake", "", hash)
	h.c.markSeriesGrabImported(h.ctx, id, hash, name)
	h.c.recordImportedHash(h.ctx, hash, name, 0)
	h.c.noteImportUnmatched(h.ctx, hash, name, 2)

	if err := h.c.GrabForSeries(h.ctx, id, "Fake", magnetFor(name), name, GrabScope{Season: 3}); err != nil {
		t.Fatal(err)
	}
	if n := newestGrabUnmatched(t, h, hash); n != 2 {
		t.Errorf("the re-grab's unmatched mark = %d, want the earlier grab's 2", n)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	reviews, _ := h.c.ListReviews(h.ctx)
	if len(reviews) != 1 {
		t.Fatalf("premise: held (reviews %+v)", reviews)
	}
	var maps []FileMapping
	for i := 0; i < 8; i++ {
		maps = append(maps, FileMapping{RelPath: nwtFiles[i], Season: 3, Episodes: []int{i + 1}})
	}
	if _, err := h.c.ImportReviewMapped(h.ctx, reviews[0].ID, id, maps); err != nil {
		t.Fatal(err)
	}
	for _, r := range meetSeedGoal(t, h, hash) {
		if r.deleteData {
			t.Fatalf("removed with its data, taking the files nobody imported: %+v", r)
		}
	}
}

// Map files… notes how many of the download's episode videos it left out — not samples,
// nor anything too small to be an episode — so a partly mapped pack keeps seeding and a
// fully mapped one is free to go at its goal.
func TestMapFilesNotesUnmappedVideos(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mapped int
		want   int
	}{
		{"eight of ten", 8, 2},
		{"all ten", 10, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, id := newTitleHarness(t, nwtShow(nwtStale))
			hash, name, dir := dropDownload(t, h, nwtPack, nwtFiles)
			// Neither of these is an episode the import would have looked at.
			writeSparse(t, filepath.Join(dir, "Nobody.Wants.This.S03E01.sample.mkv"), 70<<20)
			writeSparse(t, filepath.Join(dir, "Featurette.mkv"), 1<<20)
			h.c.recordSeriesGrab(h.ctx, id, name, "Fake", "", hash)
			h.c.ImportSeriesDownloads(h.ctx)
			reviews, _ := h.c.ListReviews(h.ctx)
			if len(reviews) != 1 {
				t.Fatalf("premise: held (reviews %+v)", reviews)
			}
			var maps []FileMapping
			for i := 0; i < tc.mapped; i++ {
				maps = append(maps, FileMapping{RelPath: nwtFiles[i], Season: 3, Episodes: []int{i + 1}})
			}
			if _, err := h.c.ImportReviewMapped(h.ctx, reviews[0].ID, id, maps); err != nil {
				t.Fatal(err)
			}
			if n := newestGrabUnmatched(t, h, hash); n != tc.want {
				t.Errorf("unmatched mark = %d, want %d", n, tc.want)
			}
		})
	}
}

// Dismissed, then grabbed again by hand and imported: the dismissal no longer stops the
// sweep filling a gap the pack can fill.
func TestDismissThenRegrabStillFillsGaps(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	hash, name, _ := dropDownload(t, h, nwtPack, nwtFiles)
	h.c.recordSeriesGrab(h.ctx, id, name, "Fake", "", hash)
	h.c.ImportSeriesDownloads(h.ctx)
	reviews, _ := h.c.ListReviews(h.ctx)
	if len(reviews) != 1 {
		t.Fatalf("premise: held (reviews %+v)", reviews)
	}
	if err := h.c.DismissReview(h.ctx, reviews[0].ID); err != nil {
		t.Fatal(err)
	}

	// The listing is corrected, and the user grabs the pack again.
	serveListing(t, h, nwtShow(nwtFixed))
	if _, rr, err := h.c.series.Refresh(h.ctx, id, series.RefreshOptions{}); err != nil || rr.FetchErr != nil {
		t.Fatal(err, rr.FetchErr)
	}
	if err := h.c.GrabForSeries(h.ctx, id, "Fake", magnetFor(name), name, GrabScope{Season: 3}); err != nil {
		t.Fatal(err)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if got := filledEpisodes(t, h, id); got != 10 {
		t.Fatalf("premise: the re-grab imported %d episodes, want 10", got)
	}

	// An episode's file is deleted; the next sweep fills it from the pack.
	if err := h.c.series.DeleteEpisodeFile(h.ctx, id, 3, 5); err != nil {
		t.Fatal(err)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if got := describeSeason(t, h, id, 3, hash, nwtFiles); got != "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10" {
		t.Errorf("gap not filled after the re-grab: %s", got)
	}
}

// failingMeta is a metadata source that can't be reached.
type failingMeta struct{}

func (failingMeta) Available() bool { return true }
func (failingMeta) SearchSeries(context.Context, string) ([]metadata.SeriesResult, error) {
	return nil, nil
}
func (failingMeta) GetSeries(context.Context, int) (*metadata.SeriesDetails, error) {
	return nil, errors.New("tmdb: 503 Service Unavailable")
}

// A refresh whose source couldn't be read isn't a refresh: it says so in the log, the
// import isn't rerun on a listing as stale as before, and the pack is held as it would be.
func TestTitleConflictRefreshFetchFailure(t *testing.T) {
	h, id := newTitleHarness(t, nwtShow(nwtStale))
	var logs bytes.Buffer
	h.c.log = slog.New(slog.NewTextHandler(&logs, nil))
	h.c.series = series.NewService(h.c.db, failingMeta{}, t.TempDir(), h.c.log)
	dropDownload(t, h, nwtPack, nwtFiles)
	s, _ := h.c.series.Get(h.ctx, id)
	if h.c.refreshForTitleConflict(h.ctx, s, "somehash", nwtPack) {
		t.Error("a failed fetch counted as a refresh")
	}
	if !strings.Contains(logs.String(), "refresh failed") {
		t.Errorf("no refresh-failed warning in the log:\n%s", logs.String())
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if reviews, _ := h.c.ListReviews(h.ctx); len(reviews) != 1 || reviews[0].ReasonCode != ReasonTitleConflict {
		t.Errorf("reviews = %+v, want the pack held", reviews)
	}
}
