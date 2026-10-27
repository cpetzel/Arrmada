package automation

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// lutherTMDB is previewMeta's ended show: five seasons of four 50-minute episodes.
const lutherTMDB = 1426

// lutherFloorProfile makes the default TV profile one with a 5 Mb/s floor at 1080p, so a
// complete pack of 14.4 GB over 1000 minutes (about 2 Mb/s) is in the avoided tier.
func lutherFloorProfile(t *testing.T, h *stallHarness) {
	t.Helper()
	createDefault(t, h.c.quality, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "Floor",
		Ideal: &quality.IdealFile{Bitrate: map[string]quality.BitrateWindow{"1080p": {Min: 5}}}})
}

const lutherCleanS01 = "Luther.S01.1080p.BluRay.x264-GRP" // 8 GB over 200 minutes: about 5.7 Mb/s

// The Luther request with only the starved complete pack on offer: the header names every
// season the pack brings, and the Why says plainly it's a last resort and what it falls
// short on — not "One pack covers the season", not Decide's generic line.
func TestPreviewSeriesAvoidedCompletePack(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	h.ix.offerSized(map[string]float64{lutherStarved: 14.4})
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Key: "request:7"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Kind != "complete series" {
		t.Fatalf("preview = %+v", p)
	}
	if !reflect.DeepEqual(p.CoveredSeasons, []int{1, 2, 3, 4, 5}) || p.SeasonsNote != "" {
		t.Fatalf("covered %v, note %q; want seasons 1–5 in the header and no note", p.CoveredSeasons, p.SeasonsNote)
	}
	want := "Nothing better was found — this is the only release that fits, and it has a bitrate under your 5 Mbps floor, which you avoid"
	if len(p.Why) == 0 || p.Why[0] != want {
		t.Fatalf("why = %q\nwant first %q", p.Why, want)
	}
	for _, w := range p.Why {
		if w == quality.WhyOnlyAvoided {
			t.Fatalf("why repeats Decide's generic line: %q", p.Why)
		}
	}
}

func lutherPack(sn int) string { return "Luther.S0" + strconv.Itoa(sn) + ".1080p.BluRay.x264-GRP" }

// The request/approval path. The broad query returns the starved complete pack and Season
// 1's clean pack; Seasons 2–5's clean packs only come back when a search asks for that
// season, as on a real indexer's single page. The preview searches every season asked for,
// so it plans those packs — not the starved one — and approving grabs them.
func TestPreviewSeriesSearchesEverySeasonAskedFor(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	h.ix.offerSized(map[string]float64{lutherStarved: 14.4, lutherCleanS01: 8})
	for sn := 2; sn <= 5; sn++ {
		h.ix.offerForSeason(sn, map[string]float64{lutherPack(sn): 8})
	}
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Seasons: []int{1, 2, 3, 4, 5}, Key: "request:8"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Title != lutherCleanS01 {
		t.Fatalf("preview = %+v", p)
	}
	if !reflect.DeepEqual(p.CoveredSeasons, []int{1}) || p.SeasonsNote != "Releases found for 4 more seasons" || p.Queries != 6 {
		t.Fatalf("covered %v, note %q, queries %d", p.CoveredSeasons, p.SeasonsNote, p.Queries)
	}
	if strings.Contains(strings.Join(p.Why, " "), "avoid") {
		t.Fatalf("why = %q", p.Why)
	}

	// Approve & grab: the previewed Season 1 pack, then the search for the rest takes the
	// clean Seasons 2–5 packs. The starved pack is never grabbed.
	off := false
	sr, err := h.c.series.AddWith(h.ctx, lutherTMDB, "", series.AddOptions{Monitored: true, MonitorNewSeasons: &off})
	if err != nil {
		t.Fatal(err)
	}
	out, err := h.c.GrabPreviewed(h.ctx, ReleaseKindSeries, sr.ID, p.Tokens, "request:8")
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(h.qbit.callLog(), "\n")
	for sn := 1; sn <= 5; sn++ {
		if !strings.Contains(calls, hashFor(lutherPack(sn))) {
			t.Errorf("approval didn't grab %s (outcome %+v)", lutherPack(sn), out)
		}
	}
	if strings.Contains(calls, hashFor(lutherStarved)) {
		t.Errorf("approval grabbed the starved complete pack (outcome %+v)", out)
	}
}

// When the pick is avoided, "better releases were found" counts only real alternatives:
// clean, seeded releases that bring a season asked for — not a dead torrent, and not a
// season the request didn't ask for.
func TestPreviewSeriesAvoidedPickCountsRealAlternatives(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	const (
		starvedS01 = "Luther.S01.1080p.WEB.x264-TINY" // 0.5 GB over 200 minutes: under the floor
		deadS02    = "Luther.S02.1080p.WEB-DL.x264-OTHER"
	)
	h.ix.offerSized(map[string]float64{starvedS01: 0.5, lutherPack(2): 8, lutherPack(3): 8, deadS02: 8})
	h.ix.mu.Lock()
	h.ix.dead = map[string]bool{deadS02: true}
	h.ix.mu.Unlock()
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Seasons: []int{1, 2}, Key: "request:10"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Title != starvedS01 {
		t.Fatalf("preview = %+v", p)
	}
	want := "1 better release was found, but none of them covers everything asked for — this one fills the rest, and it has a bitrate under your 5 Mbps floor, which you avoid"
	if len(p.Why) == 0 || p.Why[0] != want {
		t.Fatalf("why = %q\nwant first %q", p.Why, want)
	}
}

// Clean season packs for every season: the starved complete pack isn't touched, and the
// preview is Season 1's pack alone, with the rest found too.
func TestPreviewSeriesCleanPacksBeatStarvedComplete(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	offer := map[string]float64{lutherStarved: 14.4}
	for sn := 1; sn <= 5; sn++ {
		offer[lutherPack(sn)] = 8
	}
	h.ix.offerSized(offer)
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Key: "request:9"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Title != lutherCleanS01 {
		t.Fatalf("preview = %+v", p)
	}
	if !reflect.DeepEqual(p.CoveredSeasons, []int{1}) || strings.Contains(strings.Join(p.Why, " "), "avoid") ||
		p.SeasonsNote != "Releases found for 4 more seasons" {
		t.Fatalf("covered %v, why %q, note %q", p.CoveredSeasons, p.Why, p.SeasonsNote)
	}
}
