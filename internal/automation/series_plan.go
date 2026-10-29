package automation

import (
	"context"
	"log/slog"
	"time"

	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/series"
)

// planOpts says who a grab plan is for. The zero value is the automatic sweep, whose
// passes are the battle-tested ones; the scoped options only ever narrow them.
type planOpts struct {
	// Scoped: a user's click on one season or episode rather than the sweep. Every
	// multi-season or complete pack must then also be proportionate to what's wanted, and
	// a complete pack must be worth its size — a click on Season 3 must never pull a box set.
	Scoped bool
	// EpisodesOnly: take single- or multi-episode releases and nothing else. An episode
	// click asks for one file; any pack, however small the season, is more than that.
	EpisodesOnly bool
	// AllowPackFallback: a scoped plan may still take an oversized (but proportionate)
	// pack as the last resort when no single episode covers a gap. The sweep always may.
	AllowPackFallback bool
}

// planInput is everything the planner decides from. cover and try are the only ties to
// the outside world, so the tier rules can be tested without an indexer or a client.
type planInput struct {
	eligible []quality.Evaluation // ranked best-first
	// ranking is the mode that ordered eligible (Decision.Ranking). It says which avoids put
	// a release in the avoided tier, which the plan only reaches for what's still needed.
	ranking       string
	wanted        []epKey
	seriesSeasons map[int]bool
	counts        map[int]int
	ended         bool
	opts          planOpts
	// inFlight are the seasons a download in progress holds (fallbackHold). A lower-tier
	// pack that would mostly re-fetch them waits for it to import (planner.mustWait).
	inFlight map[int]bool
	// cover reports which of the still-needed episodes a release would fill.
	cover func(r parser.Release, needed map[epKey]bool) []epKey
	// try grabs a release and reports whether it's now in flight (freshly grabbed, taken
	// earlier this pass, or still pending from an earlier one). Only then are its episodes
	// counted as covered.
	try func(name, label string) bool
	// waited, optional, hears of each fallback pack held back this pass (planner.mustWait).
	waited func(name string)

	log   *slog.Logger // optional
	title string       // for the log
}

// planSeriesGrabs chooses which releases fill the wanted episodes, tier by tier, and
// returns what it couldn't cover (sorted). It's the missing sweep's selection, shared with
// the quick Grab/Replace buttons so a click obeys the same pack rules the sweep does.
//
// The releases are planned in quality tiers (quality.GrabTier): clean releases first, then
// dead torrents, then the avoided tier (under the bitrate floor, an avoided format,
// language or Unwanted item). Decide never ranks an avoided release above a clean one, but
// the passes below pick by kind and coverage, not rank — so without the tiers a starved
// complete pack beat good season packs simply because it covered more. Avoided still means
// "last resort", never "reject": a gap nothing clean covers is filled from the avoided tier.
//
// The order is: the sensible passes (complete pack, packs worth their size, single
// episodes) tier by tier through every tier, and only then the oversized-pack last resort,
// tier by tier. So an oversized pack never beats a single episode from any tier — a whole
// series for one missing episode just because the episode was avoided would be far worse
// than the episode.
func planSeriesGrabs(in planInput) []epKey {
	p := &planner{in: in, needed: setOf(in.wanted), held: map[epKey]bool{}, failed: map[string]bool{}, waiting: map[string]bool{}}
	if len(p.needed) == 0 {
		return nil
	}
	p.log = in.log
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	tiers := grabTiers(in.eligible, in.ranking)
	for _, t := range tiers {
		if len(p.needed) == 0 {
			break
		}
		p.sensiblePasses(t)
	}
	// Pass 4 — last resort. Anything still missing had no single-episode release in any
	// tier either, so take an oversized pack rather than leave the gap: an inefficient grab
	// beats none. A clean one still comes before anything avoided.
	scoped := in.opts.Scoped
	if len(p.needed) > 0 && p.packsOK() && (!scoped || in.opts.AllowPackFallback) {
		for _, t := range tiers {
			if len(p.needed) == 0 {
				break
			}
			p.takePacks(t, false)
		}
	}
	return sortedKeys(p.needed)
}

// planTierSet is one tier's releases, in Decide's order. lower: a tier below the clean,
// live one (dead torrents, the avoided tier).
type planTierSet struct {
	evs   []quality.Evaluation
	lower bool
}

// grabTiers splits the eligible releases by quality.GrabTier, best tier first, each keeping
// Decide's order — so within a tier the first complete pack is still the best-ranked one.
func grabTiers(eligible []quality.Evaluation, ranking string) []planTierSet {
	var by [4][]quality.Evaluation
	for _, ev := range eligible {
		t := quality.GrabTier(ranking, ev)
		by[t] = append(by[t], ev)
	}
	var out []planTierSet
	for t, evs := range by {
		if len(evs) > 0 {
			out = append(out, planTierSet{evs: evs, lower: t > 0})
		}
	}
	return out
}

// planner is one planSeriesGrabs run.
type planner struct {
	in     planInput
	log    *slog.Logger
	needed map[epKey]bool
	// held is what this pass's grabs hold — fresh grabs and ones still in flight from an
	// earlier pass alike. A fallback pack that overlaps it waits (mustWait).
	held map[epKey]bool
	// failed marks releases this pass tried and couldn't grab (indexer error, disk guard).
	// The pack passes must both skip them when re-selecting AND not treat their episodes as
	// covered — a transient error on the best pack used to delete its episodes from
	// `needed`, hiding them from every fallback pass and the anime follow-up, then
	// recording a "miss" that grew the backoff up to 12h. Kept here rather than by the
	// caller so a pack that can't be grabbed for any reason can't be re-selected forever.
	failed map[string]bool
	// waiting marks fallback packs held back this pass (mustWait), so they aren't
	// re-selected either.
	waiting map[string]bool
}

func (p *planner) packsOK() bool { return !p.in.opts.Scoped || !p.in.opts.EpisodesOnly }

// take grabs a release; when it's in flight, what it covers is held, no longer needed.
func (p *planner) take(name, label string, covers []epKey) bool {
	if !p.in.try(name, label) {
		p.failed[name] = true
		return false
	}
	for _, k := range covers {
		delete(p.needed, k)
		p.held[k] = true
	}
	return true
}

// mustWait reports whether a lower-tier pack (dead or avoided) has to wait for better
// grabs to import first: when most of what it would bring that matters is a copy of what
// those grabs are fetching — episodes another grab this pass holds, or seasons a download
// in flight holds (fallbackHold) — rather than gaps nothing else fills. Taken now, both
// copies race to the import, and when the worse one lands first its files fill those
// episodes. Left for a later pass, the better files are on disk first. When the gaps are
// the larger part, waiting would cost more than the duplicate download, so it's taken.
// A clean pack never waits: it's no worse than what it would duplicate.
func (p *planner) mustWait(r parser.Release) bool {
	overlap := setOf(p.in.cover(r, p.held))
	for sn := range packSeasonsOf(r, p.in.seriesSeasons) {
		if !p.in.inFlight[sn] {
			continue
		}
		n := max(p.in.counts[sn], 1)
		for e := 1; e <= n; e++ {
			overlap[epKey{sn, e}] = true
		}
	}
	gap := len(p.in.cover(r, p.needed))
	return len(overlap) > gap
}

// wait holds a fallback pack back for this pass, saying why.
func (p *planner) wait(r parser.Release, name string) {
	p.waiting[name] = true
	if p.in.waited != nil {
		p.in.waited(name)
	}
	p.log.Info("series: not taking a fallback pack yet — waiting for the better releases to import first",
		"series", p.in.title, "release", r.Title)
}

// sensiblePasses runs passes 1–3 over one tier, deleting from needed what they cover.
func (p *planner) sensiblePasses(t planTierSet) {
	in := p.in
	scoped := in.opts.Scoped

	// A whole-show / multi-season pack only makes sense once the show has actually
	// finished — for a still-running series we stick to single-season packs so each new
	// season is grabbed cleanly as its own release.

	// Pass 1 — a complete-series pack that covers every needed season (ended shows only).
	// In a later tier, needed is what the better tiers left: the pack must be mostly useful
	// for that, and must not bring what they're already fetching (mustWait).
	neededSeasons := seasonsOf(p.needed)
	if in.ended && p.packsOK() {
		for _, ev := range t.evs {
			r := ev.Candidate.Release
			if r.Kind() != parser.KindCompleteShow {
				continue
			}
			if !coversAllSeasons(r, neededSeasons, in.seriesSeasons) {
				continue
			}
			// Covering what's needed isn't enough — the pack has to be mostly useful.
			// Otherwise one missing episode pulls down an entire six-season show.
			if !packIsProportionate(neededSeasons, packSeasonsOf(r, in.seriesSeasons)) {
				p.log.Info("series: skipping complete-series pack — too little of it is needed",
					"series", in.title, "release", r.Title,
					"needed_seasons", len(neededSeasons), "pack_seasons", len(packSeasonsOf(r, in.seriesSeasons)))
				continue
			}
			// A click names one season, so "half the seasons are wanted" can still mean a
			// whole second season downloaded for one gap. Weigh it by episodes as well.
			if scoped && !packIsWorthIt(r, len(p.needed), in.seriesSeasons, in.counts) {
				continue
			}
			if t.lower && p.mustWait(r) {
				p.wait(r, ev.Candidate.Name)
				continue
			}
			// The pack covers everything wanted: nothing is left uncovered. Reporting it
			// as remaining made the anime absolute-number follow-up grab single episodes
			// the pack already contains.
			if !p.take(ev.Candidate.Name, "complete series", sortedKeys(p.needed)) {
				continue // this one couldn't be grabbed — try the next complete pack
			}
			return
		}
	}

	// Pass 2 — packs, greedily taking the one that covers the most still-needed episodes,
	// only while it's mostly useful (worth it). The oversized ones wait for the last resort.
	if p.packsOK() {
		p.takePacks(t, true)
	}

	// Pass 3 — individual episodes for whatever's left. Cheaper and more targeted than a
	// pack when only a few are missing, which is why the disproportionate packs were held
	// back above.
	for _, ev := range t.evs {
		if len(p.needed) == 0 {
			break
		}
		r := ev.Candidate.Release
		if r.Kind() != parser.KindEpisode {
			continue
		}
		covered := in.cover(r, p.needed)
		if len(covered) == 0 {
			continue
		}
		p.take(ev.Candidate.Name, "episode", covered) // a failed grab leaves its episodes needed
	}
}

// takePacks greedily takes, from one tier, the pack that covers the most still-needed
// episodes, again and again. Ended shows may take multi-season (or leftover complete-show)
// packs so a multi-season pack beats separate season packs; running shows are restricted
// to single-season packs.
//
// worthItOnly: only packs that are mostly useful (pass 2). Without it (the last resort) a
// gap is never left unfilled just because the only release covering it is a big pack.
func (p *planner) takePacks(t planTierSet, worthItOnly bool) {
	in := p.in
	scoped := in.opts.Scoped
	for {
		var best *quality.Evaluation
		var bestCover int
		for i := range t.evs {
			name := t.evs[i].Candidate.Name
			if p.failed[name] || p.waiting[name] {
				continue // couldn't be grabbed, or waits, this pass — re-selecting it would loop
			}
			r := t.evs[i].Candidate.Release
			if !isPackTier(r.Kind(), in.ended) {
				continue
			}
			// A click never takes more seasons than it has a use for, even as a last
			// resort: an inefficient grab is the sweep's call to make, not a button's.
			if scoped && !packIsProportionate(seasonsOf(p.needed), packSeasonsOf(r, in.seriesSeasons)) {
				continue
			}
			n := len(in.cover(r, p.needed))
			if n == 0 {
				continue
			}
			if worthItOnly && !packIsWorthIt(r, n, in.seriesSeasons, in.counts) {
				continue
			}
			if n > bestCover {
				bestCover, best = n, &t.evs[i]
			}
		}
		if best == nil || bestCover == 0 {
			return
		}
		r := best.Candidate.Release
		if t.lower && p.mustWait(r) {
			p.wait(r, best.Candidate.Name)
			continue
		}
		// A failed grab leaves its episodes needed; the failed-set skips it next lap.
		p.take(best.Candidate.Name, "pack", in.cover(r, p.needed))
	}
}

// seriesGrabber makes the grabs a plan asks for and remembers what it did, so the plan
// can't double-grab and the caller can report it.
type seriesGrabber struct {
	c       *Coordinator
	s       series.Series
	profile string
	byName  map[string]indexer.Release
	pending map[string]bool
	// manual and scope are recorded on each grab: whether the user's say-so lets the import
	// skip the quality gate, and for which episodes. The sweep's grabs are neither.
	manual bool
	scope  GrabScope
	// scopeOf overrides scope for single releases: a special found by its title is
	// recorded as grabbed for that special.
	scopeOf map[string]GrabScope

	grabbed map[string]bool // by download URL
	// grabbedGB tracks what this pass has already committed, so a series with many missing
	// seasons can't queue past the free space by checking each pack against the same
	// (pre-download) free-space reading.
	grabbedGB   float64
	n           int
	titles      []string
	pendingHits int
}

func (c *Coordinator) newSeriesGrabber(s series.Series, profile string, byName map[string]indexer.Release, pending map[string]bool, manual bool, scope GrabScope) *seriesGrabber {
	return &seriesGrabber{c: c, s: s, profile: profile, byName: byName, pending: pending,
		manual: manual, scope: scope, grabbed: map[string]bool{}}
}

// try grabs one release and returns whether it is now in flight — freshly grabbed, a
// duplicate of one this pass already took, or pending from an earlier sweep. Only then may
// the caller count its episodes as covered.
func (g *seriesGrabber) try(ctx context.Context, name, label string) bool {
	c, s := g.c, g.s
	ctx = c.forSeries(ctx, s) // the space check and the save path are the show's folder's
	rel := g.byName[name]
	if rel.DownloadURL == "" {
		return false
	}
	if g.grabbed[rel.DownloadURL] {
		return true // already taken this pass — its episodes are covered
	}
	if g.pending[normTitle(rel.Title)] {
		// Already grabbed and still in flight. Grabbing it again just downloads the same
		// bytes twice and stacks a duplicate torrent in the client.
		c.log.Info("series: skipping grab — already grabbed and still importing",
			"series", s.Title, "release", rel.Title)
		notesFrom(ctx).mark(rel.Title, DropPending)
		g.pendingHits++
		return true
	}
	// Space guard — the movie path has had this; TV (where packs are far bigger) did not.
	if !c.diskOKFor(ctx, rel.Indexer, g.grabbedGB+rel.SizeGB()) {
		c.log.Warn("series: skipping grab — not enough free space in the downloads dir",
			"series", s.Title, "release", rel.Title, "release_gb", rel.SizeGB(), "already_queued_gb", g.grabbedGB)
		return false
	}
	hash, err := c.grabTo(ctx, rel.Indexer, rel.DownloadURL, rel.Title, seriesCategory)
	if err != nil {
		c.log.Warn("series: grab failed", "series", s.Title, "release", rel.Title, "err", err)
		return false
	}
	g.grabbed[rel.DownloadURL] = true
	g.n++
	g.grabbedGB += rel.SizeGB()
	g.titles = append(g.titles, rel.Title)
	c.recordSeriesGrab(withReleasePublished(ctx, rel.PublishedAt), s.ID, rel.Title, rel.Indexer, g.profile, hash)
	scope := g.scope
	if sc, ok := g.scopeOf[name]; ok {
		scope = sc
	}
	// An automatic whole-show grab is the column defaults, so there's nothing to write.
	if g.manual || scope != WholeShow {
		c.markGrab(ctx, hash, g.manual, scope)
	}
	c.series.AddEvent(ctx, s.ID, "grabbed", label+": "+rel.Title+" · "+rel.Indexer)
	c.log.Info("series: grabbing", "series", s.Title, "release", rel.Title, "tier", label)
	return true
}

// fallbackHold is the seasons a lower-tier fallback pack must not bring yet
// (planInput.inFlight): those an Arrmada grab of the show is actively downloading. Only
// while the hold can end soon — a grab that has stopped moving, or is older than
// fallbackHoldMax, holds nothing, or a paused torrent or a dead swarm under the stall
// window would keep the gap open for good. exclude names releases (normTitle) that don't
// count: the stall fail-over's own stalled grab, which the replacement is for. Nothing is
// held when the record can't be read: the pack is then taken, as before the hold existed.
func (c *Coordinator) fallbackHold(ctx context.Context, s series.Series, exclude map[string]bool) map[int]bool {
	acqs, err := c.Active(ctx, "series", s.ID)
	if err != nil {
		return nil
	}
	_, seriesSeasons := wantedEpisodes(s)
	now := c.clock()
	held := map[int]bool{}
	for _, a := range acqs {
		if exclude[normTitle(a.Title)] || !holdsFallback(a, now) {
			continue
		}
		scope := a.Scope
		if scope == "" {
			scope = seriesAcqScope(a.Title, s)
		}
		sn, whole := scopeSeason(scope)
		if !whole {
			held[sn] = true
			continue
		}
		for sn := range seriesSeasons {
			held[sn] = true
		}
	}
	return held
}

// The bounds on fallbackHold: a grab holds its seasons while it moved in the last
// fallbackHoldIdle (or was grabbed that recently and hasn't been seen moving yet), and
// never past fallbackHoldMax after it was grabbed.
const (
	fallbackHoldIdle = 2 * time.Hour
	fallbackHoldMax  = 24 * time.Hour
)

// holdsFallback reports whether in-flight grab a still holds back a fallback pack.
func holdsFallback(a Acquisition, now time.Time) bool {
	if a.MediaType != "series" || a.Status == grabStatusHeld {
		return false
	}
	switch a.Phase {
	case phaseComplete, phaseMissing, "seeding", "error", "paused", "stalled":
		return false // finished, gone, or not moving
	}
	grabbed := parseTime(a.GrabbedAt)
	if grabbed.IsZero() || now.Sub(grabbed) > fallbackHoldMax {
		return false
	}
	last := a.ProgressAt
	if last.IsZero() {
		last = grabbed
	}
	return now.Sub(last) <= fallbackHoldIdle
}
