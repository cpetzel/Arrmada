package automation

import (
	"reflect"
	"testing"

	"github.com/tristenlammi/arrmada/internal/quality"
)

// The Luther request: an ended five-season show whose best-covering release was a
// complete pack under the 1080p bitrate floor (14.4 GB for ~20 hours, about 1.7 Mb/s).
const (
	lutherStarved  = "Luther (2010) S01-S05 COMPLETE 1080p H265 EAC3 2CH-MNKYDDL"
	lutherComplete = "Luther.2010.Complete.Series.1080p.BluRay.x264-GRP"
)

func lutherPacks(seasons ...int) []string {
	var out []string
	for _, sn := range seasons {
		out = append(out, "Luther.S0"+string(rune('0'+sn))+".1080p.BluRay.x264-GRP")
	}
	return out
}

func allEpisodes(counts map[int]int) []epKey {
	var out []epKey
	for sn := 1; sn <= len(counts); sn++ {
		for e := 1; e <= counts[sn]; e++ {
			out = append(out, epKey{sn, e})
		}
	}
	return out
}

func s3Episodes(n int) []string {
	var out []string
	for e := 1; e <= n; e++ {
		out = append(out, "Show.S03E"+string(rune('0'+e/10))+string(rune('0'+e%10))+".1080p")
	}
	return out
}

// tierCase is a planner scenario together with what main grabbed (before) and what the
// tiered plan grabs (after), both as "label:name" in order.
type tierCase struct {
	planCase
	opts          planOpts
	before, after []string
	left          int // episodes left uncovered after
}

func strs(xs ...[]string) []string {
	var out []string
	for _, x := range xs {
		out = append(out, x...)
	}
	return out
}

func labelled(label string, names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, label+":"+n)
	}
	return out
}

func set(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// Every case is ranked as Decide hands it over: the clean tier first, the avoided tier
// last. "before" is main's planner (legacyPlan replays it), which picked by kind and
// coverage alone, so a release in the avoided tier won whenever it covered more.
var tierCases = []tierCase{
	{
		planCase: planCase{name: "Luther: clean season packs for every season beat the starved complete pack",
			eligible: append(lutherPacks(1, 2, 3, 4, 5), lutherStarved),
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			avoided: set(lutherStarved)},
		before: labelled("complete series", lutherStarved),
		after:  labelled("pack", lutherPacks(1, 2, 3, 4, 5)...),
	},
	{
		// The clean packs fill S1–S3. For S4–S5 the complete pack is all there is: 2 of its
		// 5 seasons are wanted, so it's neither proportionate (pass 1) nor worth it (pass 2),
		// and only the last resort (pass 4) would take it. It also brings S1–S3, which the
		// clean packs are fetching right now, so it waits: grabbed together, whichever
		// lands first fills S1–S3. TestPlanStarvedPackWaitsForTheBetterImports follows it
		// through the next sweeps.
		planCase: planCase{name: "Luther: clean packs for S1–S3 only → those; the starved pack waits",
			eligible: append(lutherPacks(1, 2, 3), lutherStarved),
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			avoided: set(lutherStarved)},
		before: labelled("complete series", lutherStarved),
		after:  labelled("pack", lutherPacks(1, 2, 3)...),
		left:   20,
	},
	{
		planCase: planCase{name: "Luther: only the starved complete pack → it is taken (avoided is a last resort, not a reject)",
			eligible: []string{lutherStarved},
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			avoided: set(lutherStarved)},
		before: labelled("complete series", lutherStarved),
		after:  labelled("complete series", lutherStarved),
	},
	{
		planCase: planCase{name: "Luther: a clean complete pack beats the starved one",
			eligible: []string{lutherComplete, lutherStarved},
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			avoided: set(lutherStarved)},
		before: labelled("complete series", lutherComplete),
		after:  labelled("complete series", lutherComplete),
	},
	{
		planCase: planCase{name: "Luther running (not ended): season packs, never the complete pack — unchanged",
			eligible: append(lutherPacks(1, 2, 3, 4, 5), lutherStarved),
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons,
			avoided: set(lutherStarved)},
		before: labelled("pack", lutherPacks(1, 2, 3, 4, 5)...),
		after:  labelled("pack", lutherPacks(1, 2, 3, 4, 5)...),
	},
	{
		planCase: planCase{name: "Luther running, only the starved complete pack: nothing — unchanged",
			eligible: []string{lutherStarved},
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons,
			avoided: set(lutherStarved)},
		left: 50,
	},
	{
		// A clean pack that can't be grabbed leaves its season to the avoided tier — once
		// the clean S2–S5 packs it would also bring have imported.
		planCase: planCase{name: "Luther: the S1 clean pack fails → S1 waits for the starved pack until S2–S5 import",
			eligible: append(lutherPacks(1, 2, 3, 4, 5), lutherStarved),
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			avoided: set(lutherStarved), fail: set(lutherPacks(1)...)},
		before: labelled("complete series", lutherStarved),
		after:  strs(labelled("xpack", lutherPacks(1)...), labelled("pack", lutherPacks(2, 3, 4, 5)...)),
		left:   10,
	},
	// One gap, where the only single episode is avoided or dead and the clean releases are
	// all oversized packs: the single, as on main. The last resort runs only after the
	// single-episode pass of every tier, so a whole series never beats one episode.
	{
		planCase: planCase{name: "one gap, ended: an avoided single beats a clean complete pack",
			eligible: []string{"Show.Complete.Series.1080p", "Show.S03E04.1080p.YTS"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons, ended: true,
			avoided: set("Show.S03E04.1080p.YTS")},
		before: labelled("episode", "Show.S03E04.1080p.YTS"),
		after:  labelled("episode", "Show.S03E04.1080p.YTS"),
	},
	{
		planCase: planCase{name: "one gap, ended: a dead single beats a clean complete pack",
			eligible: []string{"Show.Complete.Series.1080p", "Show.S03E04.1080p"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons, ended: true,
			dead: set("Show.S03E04.1080p")},
		before: labelled("episode", "Show.S03E04.1080p"),
		after:  labelled("episode", "Show.S03E04.1080p"),
	},
	{
		planCase: planCase{name: "one gap, running: an avoided single beats a clean season pack",
			eligible: []string{"Show.S03.1080p", "Show.S03E04.1080p.YTS"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons,
			avoided: set("Show.S03E04.1080p.YTS")},
		before: labelled("episode", "Show.S03E04.1080p.YTS"),
		after:  labelled("episode", "Show.S03E04.1080p.YTS"),
	},
	{
		planCase: planCase{name: "one gap, running: a dead single beats a clean season pack",
			eligible: []string{"Show.S03.1080p", "Show.S03E04.1080p"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons,
			dead: set("Show.S03E04.1080p")},
		before: labelled("episode", "Show.S03E04.1080p"),
		after:  labelled("episode", "Show.S03E04.1080p"),
	},
	{
		planCase: planCase{name: "one gap, ended: an avoided single beats a clean season pack",
			eligible: []string{"Show.S03.1080p", "Show.Complete.Series.1080p", "Show.S03E04.1080p.YTS"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons, ended: true,
			avoided: set("Show.S03E04.1080p.YTS")},
		before: labelled("episode", "Show.S03E04.1080p.YTS"),
		after:  labelled("episode", "Show.S03E04.1080p.YTS"),
	},
	{
		// No single in any tier: the clean oversized pack is still the last resort, ahead
		// of an avoided one.
		planCase: planCase{name: "one gap, no single anywhere: the clean oversized pack, not the avoided one",
			eligible: []string{"Show.S03.1080p", "Show.S03.2160p.YTS"},
			wanted:   []epKey{{3, 4}}, counts: fiveSeasons,
			avoided: set("Show.S03.2160p.YTS")},
		before: labelled("pack", "Show.S03.1080p"),
		after:  labelled("pack", "Show.S03.1080p"),
	},
	{
		// Running show: an avoided season pack no longer beats clean single episodes.
		planCase: planCase{name: "running show: clean episodes beat an avoided season pack",
			eligible: append(s3Episodes(10), "Show.S03.1080p.YTS"),
			wanted:   seasonRange(1, 10), counts: fiveSeasons,
			avoided: set("Show.S03.1080p.YTS")},
		before: labelled("pack", "Show.S03.1080p.YTS"),
		after:  labelled("episode", s3Episodes(10)...),
	},
	{
		// The same holds one tier up: a dead torrent never downloads, so it is reached only
		// after every release that can.
		planCase: planCase{name: "ended show: live season packs beat a dead complete pack",
			eligible: append(lutherPacks(1, 2, 3, 4, 5), lutherComplete),
			wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
			dead: set(lutherComplete)},
		before: labelled("complete series", lutherComplete),
		after:  labelled("pack", lutherPacks(1, 2, 3, 4, 5)...),
	},
}

func TestPlanFillsFromTheCleanTierFirst(t *testing.T) {
	for _, c := range tierCases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := legacyPlan(c.planCase)
			if len(before) == 0 {
				before = nil
			}
			if !reflect.DeepEqual(before, c.before) {
				t.Errorf("main grabbed %v, the case says %v", before, c.before)
			}
			got, left := runPlan(c.planCase, c.opts)
			if !reflect.DeepEqual(got, c.after) {
				t.Errorf("grabs = %v, want %v", got, c.after)
			}
			if len(left) != c.left {
				t.Errorf("left %d uncovered (%v), want %d", len(left), left, c.left)
			}
		})
	}
}

// The starved complete pack the first sweep held back (clean S1–S3 packs grabbed) is
// still held while those download — the sweep then wants S4–S5 only, with S1–S3 in
// flight — and taken once they've imported, for the S4–S5 nothing else covers. Its S1–S3
// copies are then refused as worse (quality's TestStarvedPackEpisodeIsNeverAnUpgrade).
func TestPlanStarvedPackWaitsForTheBetterImports(t *testing.T) {
	later := []epKey{}
	for _, k := range allEpisodes(fiveSeasons) {
		if k.season >= 4 {
			later = append(later, k)
		}
	}
	c := planCase{
		eligible: append(lutherPacks(1, 2, 3), lutherStarved),
		wanted:   later, counts: fiveSeasons, ended: true,
		avoided: set(lutherStarved), inFlight: map[int]bool{1: true, 2: true, 3: true},
	}
	if calls, left := runPlan(c, planOpts{}); len(calls) != 0 || len(left) != 20 {
		t.Errorf("S1–S3 still downloading: grabbed %v (left %d), want nothing yet", calls, len(left))
	}
	c.inFlight = nil
	calls, left := runPlan(c, planOpts{})
	if !reflect.DeepEqual(calls, labelled("pack", lutherStarved)) || len(left) != 0 {
		t.Errorf("S1–S3 imported: grabbed %v (left %d), want the starved pack for S4–S5", calls, len(left))
	}
}

// The plan's tiers are the ranking's: under v2 a negative custom score is a score, not the
// avoided tier, while classic ranking puts any avoid there.
func TestPlanTierFollowsTheRanking(t *testing.T) {
	c := planCase{
		eligible: append([]string{lutherComplete}, lutherPacks(1, 2, 3, 4, 5)...),
		wanted:   allEpisodes(fiveSeasons), counts: fiveSeasons, ended: true,
	}
	custom := func(ranking string) []string {
		c.ranking = ranking
		evs := c.evaluations()
		evs[0].Avoided, evs[0].CustomAvoided = true, true // a negative custom format only
		var calls []string
		planSeriesGrabs(planInput{
			eligible: evs, ranking: ranking, wanted: c.wanted, seriesSeasons: allSeasons(c.counts),
			counts: c.counts, ended: true, cover: coveredBy,
			try: func(name, label string) bool { calls = append(calls, label+":"+name); return true },
		})
		return calls
	}
	if got := custom(quality.RankingV2); !reflect.DeepEqual(got, labelled("complete series", lutherComplete)) {
		t.Errorf("v2: grabs = %v, want the complete pack (a custom score isn't a tier)", got)
	}
	if got := custom(quality.RankingV1); !reflect.DeepEqual(got, labelled("pack", lutherPacks(1, 2, 3, 4, 5)...)) {
		t.Errorf("classic: grabs = %v, want the season packs (any avoid is the avoided tier)", got)
	}
}

// A season click obeys the tiers too, and still never takes a disproportionate pack: with
// only the starved complete pack left for the gap, the click leaves it a gap.
func TestPlanScopedSeasonUsesTiers(t *testing.T) {
	c := planCase{
		eligible: append(lutherPacks(1), lutherStarved),
		wanted:   allEpisodes(map[int]int{1: 10}), counts: fiveSeasons, ended: true,
		avoided: set(lutherStarved),
	}
	calls, left := runPlan(c, seasonClick())
	if !reflect.DeepEqual(calls, labelled("pack", lutherPacks(1)...)) || len(left) != 0 {
		t.Errorf("grabs = %v (left %v), want the clean season pack", calls, left)
	}
	c.eligible = []string{lutherStarved}
	if calls, left := runPlan(c, seasonClick()); len(calls) != 0 || len(left) != 10 {
		t.Errorf("grabs = %v (left %d), want nothing: a five-season pack for one season", calls, len(left))
	}
}
