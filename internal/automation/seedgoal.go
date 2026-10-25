package automation

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// The global seed goal (Settings → Downloads): a ratio and a seeding time that every
// torrent seeds to unless its indexer sets a goal of its own. Both unset (or 0) is the
// behaviour from before there was a global goal, so an install that never touches it
// seeds exactly as it did.
const (
	KeySeedRatio = "downloads_seed_ratio" // "" or 0 = no global ratio goal
	KeySeedHours = "downloads_seed_hours" // "" or 0 = no global time goal
)

// The bounds a seed goal may be set within, here and on an indexer. A ratio above 100 or
// a time above five years is a typo, not a goal; a negative one would read as "met" the
// moment a torrent finished.
const (
	MaxSeedRatio = 100.0
	MaxSeedHours = 5 * 365 * 24
)

// SeedDefault is the global seed goal. The zero value is "none set".
type SeedDefault struct {
	Ratio float64 `json:"ratio"`
	Hours int     `json:"hours"`
}

// Set reports whether there is a global goal at all.
func (d SeedDefault) Set() bool { return d.Ratio > 0 || d.Hours > 0 }

// ParseSeedDefault reads the stored global goal. Anything unreadable or out of bounds
// reads as unset — never as some other goal, because a wrong goal removes torrents.
func ParseSeedDefault(ratioRaw, hoursRaw string) SeedDefault {
	var d SeedDefault
	if r, err := strconv.ParseFloat(strings.TrimSpace(ratioRaw), 64); err == nil && ValidSeedRatio(r) {
		d.Ratio = r
	}
	if h, err := strconv.Atoi(strings.TrimSpace(hoursRaw)); err == nil && ValidSeedHours(h) {
		d.Hours = h
	}
	return d
}

// ValidSeedRatio and ValidSeedHours are the bounds the settings and indexer forms check.
func ValidSeedRatio(r float64) bool {
	return !math.IsNaN(r) && !math.IsInf(r, 0) && r >= 0 && r <= MaxSeedRatio
}

func ValidSeedHours(h int) bool { return h >= 0 && h <= MaxSeedHours }

// SetSeedDefault wires the global seed goal. Asked on every seed check, so a change in
// Settings reaches every grab recorded on the global goal on the next pass.
func (c *Coordinator) SetSeedDefault(fn func(ctx context.Context) SeedDefault) { c.seedDefaultFn = fn }

func (c *Coordinator) seedDefault(ctx context.Context) SeedDefault {
	if c.seedDefaultFn == nil {
		return SeedDefault{}
	}
	return c.seedDefaultFn(ctx)
}

// Where a grab's goal came from, recorded on it when it was grabbed (grabs.seed_source,
// migration 0349). "" is a grab from before then: it keeps the goal recorded on it.
const (
	seedRecOwn     = "own"     // its indexer's own goal (or seeding off), recorded on the row
	seedRecGlobal  = "global"  // its indexer is on the global goal: read live at each check
	seedRecNone    = "none"    // its indexer seeds forever (0/0)
	seedRecDefault = "default" // a manual or unknown-indexer grab: the standard window
)

// Where the goal a torrent seeds to now comes from (SeedPolicy.Source).
const (
	SeedSourceIndexer = "indexer" // its indexer's goal, recorded when it was grabbed
	SeedSourceDefault = "default" // the standard window, recorded for a manual or unknown-indexer grab
	SeedSourceGlobal  = "global"  // the global goal, read live
	SeedSourceNone    = "none"    // no goal: seeds until someone removes it
	SeedSourceOff     = "off"     // seeding is off for its indexer: removed once imported
)

// effectiveSeedGoal is the goal a grab's torrent seeds to now.
//
// The global goal is opt-in and forward-only. Only a grab recorded on it (its indexer was
// set to "use the global default", or it was a manual grab while a global goal was set)
// follows it, and follows it live, so clearing, raising or fixing the goal reaches it. A
// grab from before the global goal existed, and a grab from an indexer at 0/0, never pick
// it up: 0/0 is "seed forever", chosen by the indexer's owner, and reading it as "use the
// global goal" would delete every such torrent the moment one was set.
//
// A grab on the global goal while none is set gets the standard window, as a manual grab
// always did.
func effectiveSeedGoal(g grab, def SeedDefault) SeedPolicy {
	switch {
	case !g.SeedEnabled:
		return SeedPolicy{Source: SeedSourceOff}
	case g.SeedSource == seedRecGlobal && def.Set():
		return SeedPolicy{Enabled: true, Ratio: def.Ratio, Hours: def.Hours, Source: SeedSourceGlobal}
	case g.SeedSource == seedRecGlobal:
		return SeedPolicy{Enabled: true, Hours: indexer.DefaultSeedHours, Source: SeedSourceGlobal, GlobalUnset: true}
	case g.SeedRatio > 0 || g.SeedHours > 0:
		src := SeedSourceIndexer
		if g.SeedSource == seedRecDefault {
			src = SeedSourceDefault
		}
		return SeedPolicy{Enabled: true, Ratio: g.SeedRatio, Hours: g.SeedHours, Source: src}
	}
	return SeedPolicy{Enabled: true, Source: SeedSourceNone}
}

// seedGoalMet reports whether a finished torrent has reached its goal, and says why in
// words for the log and the title's history.
//
// The ratio comes from the byte counters (Item.SeedRatio), never the client's own field:
// when there is no honest figure (-1) only the time goal may end the seed.
func seedGoalMet(p SeedPolicy, it download.Item) (bool, string) {
	if !p.Enabled {
		return true, "seeding off — removed after import"
	}
	whose := "indexer's"
	switch p.Source {
	case SeedSourceGlobal:
		whose = "global"
	case SeedSourceDefault:
		whose = "standard"
	}
	if ratio := it.SeedRatio(); p.Ratio > 0 && ratio >= 0 && ratio >= p.Ratio {
		return true, fmt.Sprintf("seed goal met: ratio %.2f reached the %s goal of %s", ratio, whose, fmtRatio(p.Ratio))
	}
	if p.Hours > 0 && it.SeedingTime >= int64(p.Hours)*3600 {
		return true, fmt.Sprintf("seed goal met: seeded %s, the %s goal of %s", fmtSeedTime(it.SeedingTime), whose, fmtSeedTime(int64(p.Hours)*3600))
	}
	return false, ""
}

func fmtRatio(r float64) string { return strconv.FormatFloat(r, 'f', -1, 64) }

// fmtSeedTime reads a seeding time as days and hours ("14d", "3d 4h", "5h").
func fmtSeedTime(sec int64) string {
	h := sec / 3600
	d, h := h/24, h%24
	switch {
	case d > 0 && h > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case d > 0:
		return fmt.Sprintf("%dd", d)
	}
	return fmt.Sprintf("%dh", h)
}

// What the seed check says will happen to a finished torrent.
const (
	SeedOutcomeRemove     = "remove"            // goal met: removed with its data (the library has its own copy)
	SeedOutcomeRemoveKeep = "remove_keep_files" // goal met: removed from the client, its files kept (dismissed)
	SeedOutcomeSeeding    = "seeding"           // still short of its goal
	SeedOutcomeForever    = "seeds_forever"     // no goal at all
	SeedOutcomeUnmatched  = "kept_unmatched"    // goal met, but its import left files unmatched: keeps seeding
	SeedOutcomeNotYet     = "not_imported"      // Arrmada's, but not imported yet: never touched before it is
	SeedOutcomeReview     = "held_for_review"   // waiting on a decision in Review
	SeedOutcomeNotOurs    = "not_ours"          // nothing Arrmada grabbed (or only a name ties it to one): never touched
)

// SeedCheckRow is one finished torrent in the seed check: where it stands against the
// goal that applies to it, and what the next seed check will do with it.
type SeedCheckRow struct {
	Hash    string `json:"hash"`
	Name    string `json:"name"`
	Indexer string `json:"indexer,omitempty"`
	// Ratio is the computed upload ratio; RatioKnown is false when the client gave no
	// honest figure (only the time goal can end such a seed).
	Ratio          float64    `json:"ratio"`
	RatioKnown     bool       `json:"ratio_known"`
	SeedingSeconds int64      `json:"seeding_seconds"`
	Goal           SeedPolicy `json:"goal"`
	Outcome        string     `json:"outcome"`
	// Reason is why the goal counts as met, when it is.
	Reason string `json:"reason,omitempty"`
	// Later is a removal past the per-pass limit: it goes on a later pass.
	Later bool `json:"later,omitempty"`
}

// SeedCheck reports, for every finished torrent in the queue, what ManageSeeding would do
// with it under the global goal def (the saved one when def is nil, so the page can show
// what an unsaved value would do). It changes nothing.
//
// It follows ManageSeeding's own rules, in its order: only a grab whose data has landed
// (imported or dismissed) can be removed, a torrent no grab knows (or that only a name
// ties to a grab with another hash) is never touched, one held for review waits for the
// decision, one whose import left files unmatched keeps seeding, one grab removes one
// torrent, and no more than maxSeedRemovalsPerPass go in one pass.
func (c *Coordinator) SeedCheck(ctx context.Context, queue []download.Item, def *SeedDefault) ([]SeedCheckRow, error) {
	cleanup, err := c.seedCleanupGrabs(ctx)
	if err != nil {
		return nil, err
	}
	live, err := c.liveGrabs(ctx)
	if err != nil {
		return nil, err
	}
	d := c.seedDefault(ctx)
	if def != nil {
		d = *def
	}
	cm, lm := newSeedMatcher(cleanup, queue), newSeedMatcher(live, queue)
	removals := 0
	rows := []SeedCheckRow{}
	for _, it := range queue {
		if it.Transport == download.TransportUsenet || !it.Complete() {
			continue // usenet never seeds; an unfinished torrent isn't seeding yet
		}
		ratio := it.SeedRatio()
		row := SeedCheckRow{Hash: it.Hash, Name: it.Name, Ratio: math.Max(ratio, 0), RatioKnown: ratio >= 0, SeedingSeconds: it.SeedingTime}
		g := cm.match(it)
		if g == nil {
			if lg := lm.match(it); lg != nil {
				row.Indexer = lg.Indexer
				row.Goal = effectiveSeedGoal(*lg, d)
				row.Outcome = SeedOutcomeNotYet
				if lg.Status == grabStatusHeld {
					row.Outcome = SeedOutcomeReview
				}
			} else {
				row.Outcome = SeedOutcomeNotOurs
			}
			rows = append(rows, row)
			continue
		}
		row.Indexer = g.Indexer
		row.Goal = effectiveSeedGoal(*g, d)
		met, why := seedGoalMet(row.Goal, it)
		switch {
		case !met && row.Goal.Source == SeedSourceNone:
			row.Outcome = SeedOutcomeForever
		case !met:
			row.Outcome = SeedOutcomeSeeding
		case c.heldForReview(ctx, it.Hash):
			row.Outcome, row.Reason = SeedOutcomeReview, why
		case g.Status == grabStatusImported && g.ImportUnmatched > 0:
			row.Outcome, row.Reason = SeedOutcomeUnmatched, why
		default:
			row.Outcome, row.Reason = SeedOutcomeRemove, why
			if g.Status != grabStatusImported {
				row.Outcome = SeedOutcomeRemoveKeep
			}
			cm.use(g)
			removals++
			row.Later = removals > maxSeedRemovalsPerPass
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// seedCheckTimeout bounds the seed check's queue read when called from the API.
const seedCheckTimeout = 20 * time.Second

// SeedCheckQueue is SeedCheck over the live queue.
func (c *Coordinator) SeedCheckQueue(ctx context.Context, def *SeedDefault) ([]SeedCheckRow, error) {
	ctx, cancel := context.WithTimeout(ctx, seedCheckTimeout)
	defer cancel()
	queue, err := c.downloads.Queue(ctx)
	if err != nil {
		return nil, err
	}
	return c.SeedCheck(ctx, queue, def)
}
