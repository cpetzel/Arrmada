package automation

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// stallShow is a show with one ten-episode Season 3, for the stall fail-over cases.
func stallShow(t *testing.T, h *stallHarness) int64 {
	t.Helper()
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 9, Title: "Show", Year: 2020, Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	var eps []series.Episode
	for e := 1; e <= 10; e++ {
		eps = append(eps, series.Episode{SeasonNumber: 3, EpisodeNumber: e, AirDate: "2021-01-01", Monitored: true})
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{{SeasonNumber: 3, Monitored: true, Episodes: eps}}); err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

// A stalled grab is replaced even when the only replacement is a pack covering its season.
// The replacement search must not wait on the very grab it replaces — that grab held its
// season "in flight" every stall window, so the season was stuck for good.
func TestStallReplacementDoesNotWaitOnItself(t *testing.T) {
	cases := []struct {
		name, stalled, replacement string
		avoidHEVC                  bool
	}{
		{"a stalled single, the clean season pack the only alternative",
			"Show.S03E01.1080p.WEB-DL.x264-OLD", "Show.S03.1080p.WEB-DL.x264-NEW", false},
		{"a stalled clean season pack, an avoided season pack the only alternative",
			"Show.S03.1080p.WEB-DL.x264-OLD", "Show.S03.1080p.WEB-DL.x265-NEW", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newStallHarness(t)
			if tc.avoidHEVC {
				createDefault(t, h.c.quality, quality.StoredProfile{MediaType: quality.MediaSeries, Name: "no HEVC",
					Ideal: &quality.IdealFile{Codec: map[string]string{"hevc": quality.PrefAvoid}}})
			}
			id := stallShow(t, h)
			h.addStalledGrab(t, "series", id, tc.stalled, seriesCategory)
			h.ix.offer(tc.stalled, tc.replacement)
			h.observe()
			h.tick()
			if calls := strings.Join(h.qbit.callLog(), "|"); !strings.Contains(calls, "add "+magnetFor(tc.replacement)) {
				t.Errorf("the stalled grab wasn't replaced by %s: client calls %q", tc.replacement, calls)
			}
		})
	}
}

// The overlap rule: a lower-tier pack waits only when most of what it would bring that
// matters is a copy of what's being fetched; when the gaps it alone fills are the larger
// part, waiting would cost more than the duplicate, so it's taken.
func TestPlanLowerTierPackWaitsOnlyWhenMostlyOverlap(t *testing.T) {
	wantS := func(seasons ...int) []epKey {
		var out []epKey
		for _, sn := range seasons {
			for e := 1; e <= 10; e++ {
				out = append(out, epKey{sn, e})
			}
		}
		return out
	}
	cases := []struct {
		name     string
		wanted   []epKey
		inFlight map[int]bool
		want     []string
	}{
		// S4–S5 missing (20 episodes), S1 downloading (10): mostly gap — taken.
		{"S1 downloading, S4–S5 missing", wantS(4, 5), map[int]bool{1: true}, labelled("pack", lutherStarved)},
		// The reviewer's case: S2 downloading, only S5E2 missing — mostly a copy of S2, so
		// it waits while S2 is actually downloading (fallbackHold bounds that)…
		{"S2 downloading, S5E2 missing", []epKey{{5, 2}}, map[int]bool{2: true}, nil},
		// …and is taken once S2's grab no longer holds (finished, stalled or too old).
		{"S2's grab no longer holds, S5E2 missing", []epKey{{5, 2}}, nil, labelled("pack", lutherStarved)},
	}
	for _, tc := range cases {
		calls, _ := runPlan(planCase{eligible: []string{lutherStarved}, wanted: tc.wanted, counts: fiveSeasons,
			ended: true, avoided: set(lutherStarved), inFlight: tc.inFlight}, planOpts{})
		if !reflect.DeepEqual(calls, tc.want) {
			t.Errorf("%s: grabs = %v, want %v", tc.name, calls, tc.want)
		}
	}
}

// A clean pack never waits, whatever it overlaps: it's no worse than what it duplicates,
// so it behaves as on main. Here the S03 last-resort pack overlaps the S03E01 single taken
// in the same pass, and S03 is even downloading.
func TestPlanCleanFallbackNeverWaits(t *testing.T) {
	c := planCase{eligible: []string{"Show.S03E01.1080p", "Show.S03.1080p"}, wanted: []epKey{{3, 1}, {3, 5}},
		counts: fiveSeasons, inFlight: map[int]bool{3: true}}
	calls, left := runPlan(c, planOpts{})
	if want := []string{"episode:Show.S03E01.1080p", "pack:Show.S03.1080p"}; !reflect.DeepEqual(calls, want) || len(left) != 0 {
		t.Errorf("grabs = %v (left %v), want %v", calls, left, want)
	}
}

// What holds a fallback back is a grab actually downloading, and never for more than a day.
func TestHoldsFallback(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) string { return now.Add(-ago).Format("2006-01-02 15:04:05") }
	cases := []struct {
		name string
		a    Acquisition
		want bool
	}{
		{"downloading, moved just now", Acquisition{MediaType: "series", Phase: "downloading", GrabbedAt: at(5 * time.Hour), ProgressAt: now.Add(-time.Minute)}, true},
		{"just grabbed, not seen yet", Acquisition{MediaType: "series", GrabbedAt: at(10 * time.Minute)}, true},
		{"paused by the user", Acquisition{MediaType: "series", Phase: "paused", GrabbedAt: at(time.Hour), ProgressAt: now.Add(-time.Minute)}, false},
		{"stalled", Acquisition{MediaType: "series", Phase: "stalled", GrabbedAt: at(time.Hour), ProgressAt: now.Add(-time.Minute)}, false},
		{"no progress for three hours", Acquisition{MediaType: "series", Phase: "downloading", GrabbedAt: at(5 * time.Hour), ProgressAt: now.Add(-3 * time.Hour)}, false},
		{"a slow swarm, but grabbed over a day ago", Acquisition{MediaType: "series", Phase: "downloading", GrabbedAt: at(25 * time.Hour), ProgressAt: now.Add(-time.Minute)}, false},
		{"finished", Acquisition{MediaType: "series", Phase: phaseComplete, GrabbedAt: at(time.Hour)}, false},
		{"held in Review", Acquisition{MediaType: "series", Status: grabStatusHeld, Phase: "downloading", GrabbedAt: at(time.Hour), ProgressAt: now}, false},
	}
	for _, tc := range cases {
		if got := holdsFallback(tc.a, now); got != tc.want {
			t.Errorf("%s: holds = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A sweep that grabbed nothing only because a fallback pack waits is neither a miss nor a
// reset: the backoff mustn't push the wait out by hours.
func TestSeriesSweepOutcomeWaiting(t *testing.T) {
	if reset, miss := seriesSweepOutcome(nil, SearchOutcome{Searched: true, Waiting: 1}); reset || miss {
		t.Errorf("waiting: reset=%v miss=%v, want neither", reset, miss)
	}
	if reset, miss := seriesSweepOutcome(nil, SearchOutcome{Searched: true}); reset || !miss {
		t.Errorf("nothing found: reset=%v miss=%v, want a miss", reset, miss)
	}
	if reset, miss := seriesSweepOutcome(nil, SearchOutcome{Searched: true, Grabbed: 1, Waiting: 1}); !reset || miss {
		t.Errorf("grabbed and waiting: reset=%v miss=%v, want a reset", reset, miss)
	}
	// The plan's waits reach the outcome through the search notes.
	n := &searchNotes{}
	n.waited(lutherStarved)
	n.waited(lutherStarved)
	var o SearchOutcome
	o.absorb(n)
	if o.Waiting != 1 {
		t.Errorf("outcome waiting = %d, want 1", o.Waiting)
	}
}

func TestWithWaitingNote(t *testing.T) {
	if got := withWaitingNote("", 1); got != "A lower-quality pack is waiting for the downloads in progress to finish" {
		t.Errorf("alone: %q", got)
	}
	if got := withWaitingNote("3 episodes still missing", 1); got != "3 episodes still missing — a lower-quality pack is waiting for the downloads in progress to finish" {
		t.Errorf("after a note: %q", got)
	}
	if got := withWaitingNote("3 episodes still missing", 0); got != "3 episodes still missing" {
		t.Errorf("no wait: %q", got)
	}
}
