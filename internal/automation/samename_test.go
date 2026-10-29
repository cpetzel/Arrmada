package automation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/indexer"
)

// A search's grab keeps the release's publish date, which the import's same-name check
// reads (#81).
func TestSameNameGrabRecordsPublished(t *testing.T) {
	r := newSameNameRig(t)
	s, err := r.c.series.Get(r.ctx, r.old)
	if err != nil {
		t.Fatal(err)
	}
	title := "Scrubs.S02E04.1080p.WEB-DL.x264-GRP"
	r.c.grabSeriesFrom(r.ctx, s, []indexer.Release{sameNameRel(title, "2012-01-01T00:00:00Z")})
	var got string
	if err := r.c.db.QueryRow(`SELECT published_at FROM grabs WHERE title = ?`, title).Scan(&got); err != nil {
		t.Fatalf("no grab recorded: %v", err)
	}
	if got != "2012-01-01T00:00:00Z" {
		t.Errorf("published_at = %q, want 2012-01-01T00:00:00Z", got)
	}
	if p := r.c.grabPublished(r.ctx, hashFor(title), title); !p.Equal(time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("grabPublished = %v", p)
	}
	// Found by name too, when the hash is unknown.
	if p := r.c.grabPublished(r.ctx, "", title+".mkv"); p.IsZero() {
		t.Error("grabPublished by name found nothing")
	}
}

// The interactive list still lists a release a same-named show could own, saying why it
// isn't taken automatically; a release that states the year, and every release for a show
// with no same-named sibling, rank as before.
func TestSameNameInteractiveList(t *testing.T) {
	r := newSameNameRig(t)
	yearless, dated := "Scrubs.S02E04.1080p.WEB-DL.x264-GRP", "Scrubs.2001.S02E04.720p.WEB-DL.x264-GRP"
	r.ix.offer(yearless, dated, "House.S02E04.1080p.WEB-DL.x264-GRP")

	list, err := r.c.RankSeriesReleases(r.ctx, r.old, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]RankedRelease{}
	for _, rr := range list.Releases {
		rows[rr.Title] = rr
	}
	if y, ok := rows[yearless]; !ok || y.Eligible || y.Recommended ||
		!strings.Contains(y.RejectReason, "Scrubs (2001) and Scrubs (2026) share a name") {
		t.Errorf("yearless row = %+v (listed %v), want listed, not eligible, saying both shows share the name", y, ok)
	}
	if d, ok := rows[dated]; !ok || !d.Eligible || !d.Recommended {
		t.Errorf("the 2001-stating row = %+v (listed %v), want eligible and recommended", d, ok)
	}

	list, err = r.c.RankSeriesReleases(r.ctx, r.house, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Releases) != 1 || !list.Releases[0].Eligible || list.Releases[0].RejectReason != "" {
		t.Errorf("control show's list = %+v, want its one release eligible", list.Releases)
	}
}

// The quick Grab button skips a release a same-named show could own, and takes one that
// states the year.
func TestSameNameQuickGrab(t *testing.T) {
	r := newSameNameRig(t)
	r.ix.offer("Scrubs.S02E04.1080p.WEB-DL.x264-GRP")
	out, err := r.c.GrabForScope(r.ctx, r.old, SeriesScope{Season: 2, Episode: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Grabbed) != 0 || out.SameName != 1 || out.WrongShow != 0 {
		t.Errorf("yearless: grabbed %v, same name %d, wrong show %d; want nothing grabbed, one same-name skip", out.Grabbed, out.SameName, out.WrongShow)
	}
	if !strings.Contains(out.Summary(), "1 that could be a same-named show's") {
		t.Errorf("summary %q doesn't say why", out.Summary())
	}
	r.ix.offer("Scrubs.2001.S02E04.1080p.WEB-DL.x264-GRP")
	out, err = r.c.GrabForScope(r.ctx, r.old, SeriesScope{Season: 2, Episode: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Grabbed) != 1 {
		t.Errorf("year-stating: grabbed %v, want it", out.Grabbed)
	}
}

// The held download's review names both shows and offers the grabbed show's import.
func TestSameNameHoldReason(t *testing.T) {
	r := newSameNameRig(t)
	got := r.runImport(t, importCase{release: "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", grabFor: "old", added: time.Now()})
	if got != "held:same_name" {
		t.Fatalf("got %q, want held", got)
	}
	rv := reviewFor(t, r.c, hashFor("Scrubs.S02E03.2160p.WEB-DL.x265-GRP"))
	want := "Grabbed for Scrubs (2001), but Scrubs (2001) and Scrubs (2026) share a name and this release doesn't say which"
	if rv.Reason != want || rv.ExpectedID != r.old {
		t.Errorf("review = %q (expected %d), want %q for %d", rv.Reason, rv.ExpectedID, want, r.old)
	}
	// Import anyway puts it where it was grabbed for.
	if err := r.c.ImportReview(context.Background(), rv.ID, r.old, ""); err != nil {
		t.Fatalf("import anyway: %v", err)
	}
	files := r.libraryFiles(t)
	if _, ok := files["Scrubs (2001)/S02/Scrubs - S02E03 - 2160p WEB-DL.mkv"]; !ok {
		t.Errorf("after Import anyway the library is %v", files)
	}
}

// Import anyway on a same-name hold confirms the show, not the quality: a release no
// better than the file there doesn't replace it.
func TestSameNameImportAnywayKeepsQualityGate(t *testing.T) {
	r := newSameNameRig(t)
	name := "Scrubs.S02E03.480p.WEB-DL.x264-WORSE"
	if got := r.runImport(t, importCase{release: name, grabFor: "old", added: time.Now()}); got != "held:same_name" {
		t.Fatalf("got %q, want held", got)
	}
	rv := reviewFor(t, r.c, hashFor(name))
	before := r.libraryFiles(t)
	ctx, outcome := WithReviewImportOutcome(context.Background())
	if err := r.c.ImportReview(ctx, rv.ID, r.old, ""); err != nil {
		t.Fatalf("import anyway: %v", err)
	}
	// Settled, and saying so: not a silent "Imported".
	if !outcome.KeptExisting {
		t.Error("the outcome doesn't say the existing file was kept")
	}
	var status, resolution string
	if err := r.c.db.QueryRow(`SELECT status, resolution FROM import_reviews WHERE id = ?`, rv.ID).Scan(&status, &resolution); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || resolution != ResolutionKept {
		t.Errorf("review is %s/%s, want resolved/%s", status, resolution, ResolutionKept)
	}
	if !r.c.hashAlreadyImported(r.ctx, hashFor(name)) {
		t.Error("the sweep would pick the download up again")
	}
	var grabStatus string
	if err := r.c.db.QueryRow(`SELECT status FROM grabs WHERE info_hash = ?`, hashFor(name)).Scan(&grabStatus); err != nil || grabStatus != grabStatusDismissed {
		t.Errorf("grab status %q (%v), want %q so seed cleanup keeps the files", grabStatus, err, grabStatusDismissed)
	}
	after := r.libraryFiles(t)
	if len(after) != len(before) {
		t.Errorf("a worse release changed the library: %v -> %v", before, after)
	}
	if _, ok := after["Scrubs (2001)/S02/Scrubs.S02E03.720p.HDTV.x264-OLD.mkv"]; !ok {
		t.Errorf("the 720p file was replaced by a 480p one: %v", after)
	}
}

// A search's same-name skips are recorded under their own reason, not as other titles.
func TestSameNameDropCode(t *testing.T) {
	r := newSameNameRig(t)
	s, err := r.c.series.Get(r.ctx, r.old)
	if err != nil {
		t.Fatal(err)
	}
	ctx, notes := newSearchNotes(r.ctx)
	title := "Scrubs.S02E04.1080p.WEB-DL.x264-GRP"
	notes.consider([]indexer.Release{sameNameRel(title, "")})
	r.c.grabSeriesFrom(ctx, s, []indexer.Release{sameNameRel(title, "")})
	notes.mu.Lock()
	code := notes.class[title]
	notes.mu.Unlock()
	if code != DropSameName {
		t.Errorf("recorded as %q, want %q", code, DropSameName)
	}
	if l := ReasonLabel(DropSameName); !strings.Contains(l, "same-named") {
		t.Errorf("label %q", l)
	}
}

// The same-named shows are read once for a whole pass, not per show.
func TestSameNameLibraryReadOncePerPass(t *testing.T) {
	r := newSameNameRig(t)
	ctx := withSameNameLibrary(r.ctx)
	s, err := r.c.series.Get(r.ctx, r.old)
	if err != nil {
		t.Fatal(err)
	}
	if named, err := r.c.sameNamedIn(ctx, s); err != nil || len(named) != 1 {
		t.Fatalf("first read: %v, %v", named, err)
	}
	// A show added after the pass's read isn't seen by it — the next pass reads again.
	if _, err := r.c.db.Exec(`INSERT INTO series (tmdb_id, title, year, monitored) VALUES (999, 'Scrubs', 2030, 1)`); err != nil {
		t.Fatal(err)
	}
	if named, _ := r.c.sameNamedIn(ctx, s); len(named) != 1 {
		t.Errorf("the pass re-read the library: %d same-named shows", len(named))
	}
	if named, _ := r.c.sameNamedIn(r.ctx, s); len(named) != 2 {
		t.Errorf("a fresh read found %d same-named shows, want 2", len(named))
	}
}

// A same-name hold a person settled is never held again: a season pack with gaps left is
// re-read by later sweeps (to fill them), and must not come back to Needs you each time —
// whether Import anyway placed files or kept the ones there.
func TestSameNameSettledPackNotHeldAgain(t *testing.T) {
	for _, c := range []struct {
		name, release string
		files         []string
		wantPlaced    bool
	}{
		// Imported: better, and fills S02E01–E02; S02E04 stays missing.
		{"imported", "Scrubs.S02.1080p.BluRay.x264-PACK",
			[]string{"Scrubs.S02E01.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E02.1080p.BluRay.x264-PACK.mkv", "Scrubs.S02E03.1080p.BluRay.x264-PACK.mkv"}, true},
		// Kept: its one file is worse than the 720p S02E03 there; S02E01, E02, E04 stay missing.
		{"kept_existing", "Scrubs.S02.480p.WEB-DL.x264-WORSE",
			[]string{"Scrubs.S02E03.480p.WEB-DL.x264-WORSE.mkv"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newSameNameRig(t)
			if got := r.runImport(t, importCase{release: c.release, files: c.files, grabFor: "old", added: time.Now()}); got != "held:same_name" {
				t.Fatalf("got %q, want held", got)
			}
			rv := reviewFor(t, r.c, hashFor(c.release))
			ctx, outcome := WithReviewImportOutcome(context.Background())
			if err := r.c.ImportReview(ctx, rv.ID, r.old, ""); err != nil {
				t.Fatalf("import anyway: %v", err)
			}
			if outcome.KeptExisting == c.wantPlaced {
				t.Fatalf("kept existing = %v, want %v", outcome.KeptExisting, !c.wantPlaced)
			}
			if !r.c.series.SeasonHasMissing(r.ctx, r.old, 2) {
				t.Fatal("setup: season 2 must still have gaps, or the sweep won't re-read the pack")
			}
			before := r.libraryFiles(t)
			for range 2 {
				r.c.ImportSeriesDownloads(r.ctx)
			}
			if n := pendingSameName(t, r); n != 0 {
				t.Errorf("%d new same-name hold(s) after the person settled it", n)
			}
			if after := r.libraryFiles(t); len(after) != len(before) {
				t.Errorf("later sweeps changed the library: %v -> %v", before, after)
			}
		})
	}
}

func pendingSameName(t *testing.T, r *sameNameRig) int {
	t.Helper()
	var n int
	if err := r.c.db.QueryRow(`SELECT COUNT(*) FROM import_reviews WHERE reason_code = ? AND status = 'pending'`, ReasonSameName).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Kept with files it couldn't match says so.
func TestKeptExistingText(t *testing.T) {
	if got := KeptExistingText(0); got != "Kept the existing file — this release isn't better" {
		t.Errorf("%q", got)
	}
	if got := KeptExistingText(2); got != "Kept the existing file — this release isn't better; 2 files couldn't be matched and were left in the download" {
		t.Errorf("%q", got)
	}
	if got := KeptExistingText(1); !strings.Contains(got, "1 file couldn't be matched and was left") {
		t.Errorf("%q", got)
	}
}

// A failed read of the same-named shows isn't kept for the pass: the next show retries.
func TestSameNameLibraryReadRetries(t *testing.T) {
	r := newSameNameRig(t)
	s, err := r.c.series.Get(r.ctx, r.old)
	if err != nil {
		t.Fatal(err)
	}
	pass := withSameNameLibrary(r.ctx)
	dead, cancel := context.WithCancel(pass)
	cancel()
	if _, err := r.c.sameNamedIn(dead, s); err == nil {
		t.Fatal("a read under a cancelled context succeeded")
	}
	if named, err := r.c.sameNamedIn(pass, s); err != nil || len(named) != 1 {
		t.Errorf("the retry: %v, %v — want the revival", named, err)
	}
}

// A show whose same-named shows can't be read is skipped before any indexer is asked.
func TestSameNameUnreadableCostsNoIndexerQuery(t *testing.T) {
	r := newSameNameRig(t)
	r.apply(t, "unreadable")
	r.ix.offer("Scrubs.S02E04.1080p.WEB-DL.x264-GRP")
	before := r.ix.searchCount()
	if _, err := r.c.SearchSeriesNow(r.ctx, r.old); err == nil {
		t.Error("the search reported success with the same-named shows unreadable")
	}
	if _, err := r.c.GrabForScope(r.ctx, r.old, SeriesScope{Season: 2, Episode: 4}); err == nil {
		t.Error("the quick Grab reported success with the same-named shows unreadable")
	}
	if n := r.ix.searchCount() - before; n != 0 {
		t.Errorf("%d indexer queries for a show that was skipped", n)
	}
	// A show with no same-named sibling is searched as before.
	if _, err := r.c.SearchSeriesNow(r.ctx, r.house); err != nil {
		t.Errorf("control show: %v", err)
	}
	if r.ix.searchCount() == before {
		t.Error("the control show wasn't searched")
	}
}

func TestWithReleasePublished(t *testing.T) {
	ctx := WithReleasePublished(context.Background(), "2020-05-06T07:08:09+02:00")
	if got := releasePublished(ctx); got != "2020-05-06T05:08:09Z" {
		t.Errorf("releasePublished = %q", got)
	}
	if got := releasePublished(WithReleasePublished(context.Background(), "")); got != "" {
		t.Errorf("unknown date recorded as %q", got)
	}
}
