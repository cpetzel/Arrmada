package automation

import (
	"fmt"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
	"github.com/tristenlammi/arrmada/internal/movies"
)

// A film whose minimum availability is "Released" and that is in cinemas but not yet out
// on digital or disc isn't searched by the scheduled sweep or the RSS sync (almost every
// release then is a cam); once its digital date has passed it is. A person's own Search
// doesn't wait for it.
func TestSweepWaitsForTheDigitalRelease(t *testing.T) {
	h := newStallHarness(t)
	day := func(offset int) *metadata.DatedRelease {
		return &metadata.DatedRelease{Date: time.Now().AddDate(0, 0, offset).Format(time.DateOnly), Country: "US"}
	}
	cinema := day(-20)
	m, err := movies.NewRepo(h.c.db).Create(h.ctx, movies.Movie{TMDBID: 7001, Title: "Cinema Only", Year: time.Now().Year(),
		Status: "Released", Monitored: true, MinAvailability: "released",
		Extra: &movies.MovieExtra{ReleaseDate: cinema.Date, Releases: &metadata.ReleaseDates{Cinema: cinema, Digital: day(25)}}})
	if err != nil {
		t.Fatal(err)
	}
	// A proper-looking release is out there (a mislabelled cam, as often as not).
	h.ix.offer(fmt.Sprintf("Cinema.Only.%d.1080p.WEB-DL.x264", time.Now().Year()))

	h.c.RSSSync(h.ctx)
	for _, call := range h.qbit.callLog() {
		t.Fatalf("the RSS sync grabbed for a film not out on digital yet: %s", call)
	}
	before := h.ix.searchCount()
	h.c.SearchMissing(h.ctx)
	if n := h.ix.searchCount() - before; n != 0 {
		t.Fatalf("the sweep searched a film not out on digital yet (%d searches)", n)
	}
	if got := searchedCount(t, h, "movies"); got != 0 {
		t.Fatalf("%d films marked searched", got)
	}

	// Search now still searches: a person's own search doesn't wait.
	h.ix.offer() // nothing to grab, so the film is still missing afterwards
	before = h.ix.searchCount()
	if _, err := h.c.SearchMovieManual(h.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := h.ix.searchCount() - before; n != 1 {
		t.Fatalf("Search now ran %d searches, want 1", n)
	}

	// Its digital release passes: the next sweep searches it.
	if err := movies.NewRepo(h.c.db).SetReleases(h.ctx, m.ID, &metadata.ReleaseDates{Cinema: cinema, Digital: day(-1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.c.db.Exec(`UPDATE movies SET last_search_at = '', search_misses = 0 WHERE id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	before = h.ix.searchCount()
	h.c.SearchMissing(h.ctx)
	if n := h.ix.searchCount() - before; n != 1 {
		t.Fatalf("after the digital release the sweep ran %d searches, want 1", n)
	}
	// And the RSS sync now takes the release it left alone before.
	h.ix.offer(fmt.Sprintf("Cinema.Only.%d.1080p.WEB-DL.x264", time.Now().Year()))
	h.c.RSSSync(h.ctx)
	if len(h.qbit.callLog()) == 0 {
		t.Fatal("the RSS sync didn't grab once the film was out on digital")
	}
}
