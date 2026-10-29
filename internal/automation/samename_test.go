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
	if len(out.Grabbed) != 0 || out.WrongShow != 1 {
		t.Errorf("yearless: grabbed %v, wrong show %d; want nothing grabbed, one refused", out.Grabbed, out.WrongShow)
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
	if got != "held:mismatch" {
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

func TestWithReleasePublished(t *testing.T) {
	ctx := WithReleasePublished(context.Background(), "2020-05-06T07:08:09+02:00")
	if got := releasePublished(ctx); got != "2020-05-06T05:08:09Z" {
		t.Errorf("releasePublished = %q", got)
	}
	if got := releasePublished(WithReleasePublished(context.Background(), "")); got != "" {
		t.Errorf("unknown date recorded as %q", got)
	}
}
