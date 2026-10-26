package automation

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

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
	if r.ReasonCode != ReasonNumbering || r.ExpectedID != id {
		t.Errorf("held as %q for %d, want a numbering review for the show (it offers Map files…)", r.ReasonCode, r.ExpectedID)
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
