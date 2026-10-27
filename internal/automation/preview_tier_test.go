package automation

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
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

// With a clean pack for Season 1 on offer too, the plan takes it for Season 1 and the
// starved pack only for the seasons nothing clean covers — and the Why says so.
func TestPreviewSeriesAvoidedPackFillsTheRest(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	h.ix.offerSized(map[string]float64{lutherStarved: 14.4, lutherCleanS01: 8})
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Key: "request:8"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 2 {
		t.Fatalf("preview = %+v", p)
	}
	if p.Releases[0].Title != lutherCleanS01 || p.Releases[1].Title != lutherStarved {
		t.Fatalf("releases = %s, %s; want the clean S01 pack first, then the starved complete pack",
			p.Releases[0].Title, p.Releases[1].Title)
	}
	if !reflect.DeepEqual(p.CoveredSeasons, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("covered %v", p.CoveredSeasons)
	}
	want := "1 better release was found, but none of them covers everything asked for — this one fills the rest, and it has a bitrate under your 5 Mbps floor, which you avoid"
	if len(p.Why) < 2 || p.Why[0] != want || p.Why[1] != "2 packs between them cover what's asked for" {
		t.Fatalf("why = %q", p.Why)
	}
}

// Clean season packs for every season: the starved complete pack isn't touched, and the
// preview is Season 1's pack alone.
func TestPreviewSeriesCleanPacksBeatStarvedComplete(t *testing.T) {
	h := newPreviewHarness(t)
	lutherFloorProfile(t, h)
	offer := map[string]float64{lutherStarved: 14.4}
	for sn := 1; sn <= 5; sn++ {
		offer["Luther.S0"+string(rune('0'+sn))+".1080p.BluRay.x264-GRP"] = 8
	}
	h.ix.offerSized(offer)
	p, err := h.c.PreviewTitle(h.ctx, PreviewInput{MediaType: "series", TMDBID: lutherTMDB, Key: "request:9"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Outcome != PreviewFound || len(p.Releases) != 1 || p.Releases[0].Title != lutherCleanS01 {
		t.Fatalf("preview = %+v", p)
	}
	if !reflect.DeepEqual(p.CoveredSeasons, []int{1}) || strings.Contains(strings.Join(p.Why, " "), "avoid") {
		t.Fatalf("covered %v, why %q", p.CoveredSeasons, p.Why)
	}
}
