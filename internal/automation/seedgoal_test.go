package automation

import (
	"context"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/indexer"
)

// The goal a torrent seeds to: its indexer's own goal first, then the global one, then
// what it always did. Only the whole goal is inherited, and a dismissed grab keeps what
// was recorded for it.
func TestEffectiveSeedGoalPrecedence(t *testing.T) {
	global := SeedDefault{Ratio: 1, Hours: 72}
	imported := func(enabled bool, ratio float64, hours int) grab {
		return grab{SeedEnabled: enabled, SeedRatio: ratio, SeedHours: hours, Status: grabStatusImported}
	}
	cases := []struct {
		name string
		g    grab
		def  SeedDefault
		want SeedPolicy
	}{
		{"indexer goal wins over the global one", imported(true, 2, 0), global, SeedPolicy{Enabled: true, Ratio: 2, Source: SeedSourceIndexer}},
		// A ratio-only indexer must not have the global time filled in beside it: that
		// would end the seed before the tracker's ratio was reached.
		{"half an indexer goal is still the indexer's", imported(true, 0, 500), global, SeedPolicy{Enabled: true, Hours: 500, Source: SeedSourceIndexer}},
		{"an indexer with no goal takes the global one", imported(true, 0, 0), global, SeedPolicy{Enabled: true, Ratio: 1, Hours: 72, Source: SeedSourceGlobal}},
		{"no global goal: seeds forever, as before", imported(true, 0, 0), SeedDefault{}, SeedPolicy{Enabled: true, Source: SeedSourceNone}},
		{"seeding off stays off", imported(false, 0, 0), global, SeedPolicy{Source: SeedSourceOff}},
		{"a grab not imported yet shows the global goal it will get", grab{SeedEnabled: true, Status: grabStatusGrabbed}, global, SeedPolicy{Enabled: true, Ratio: 1, Hours: 72, Source: SeedSourceGlobal}},
		{"a dismissed grab keeps what was recorded", grab{SeedEnabled: true, Status: grabStatusDismissed}, global, SeedPolicy{Enabled: true, Source: SeedSourceNone}},
	}
	for _, tc := range cases {
		if got := effectiveSeedGoal(tc.g, tc.def); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestParseSeedDefaultRejectsWhatIsntAGoal(t *testing.T) {
	cases := []struct {
		ratio, hours string
		want         SeedDefault
	}{
		{"", "", SeedDefault{}},
		{"2", "336", SeedDefault{Ratio: 2, Hours: 336}},
		{" 1.5 ", "0", SeedDefault{Ratio: 1.5}},
		{"-1", "-5", SeedDefault{}}, // a negative goal would be "met" at once
		{"NaN", "x", SeedDefault{}},
		{"101", "999999", SeedDefault{}},
	}
	for _, tc := range cases {
		if got := ParseSeedDefault(tc.ratio, tc.hours); got != tc.want {
			t.Errorf("ParseSeedDefault(%q, %q) = %+v, want %+v", tc.ratio, tc.hours, got, tc.want)
		}
	}
	if (SeedDefault{}).Set() {
		t.Error("the zero goal reads as set")
	}
}

// seedTorrent puts a finished torrent in the fake client with the given ratio (uploaded
// over 1 GB downloaded) and seeding time.
func (h *lifecycleHarness) seedTorrent(hash, name string, uploadedGB int64, seededHours int) {
	defer h.c.downloads.InvalidateSnapshot()
	h.qbit.mu.Lock()
	defer h.qbit.mu.Unlock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hash, "name": name, "state": "uploading", "progress": 1.0, "amount_left": 0,
		"size": 1 << 30, "completed": 1 << 30, "downloaded": 1 << 30, "uploaded": uploadedGB << 30,
		"seeding_time": seededHours * 3600, "category": seriesCategory,
	})
}

// grabWith records a series grab with the given seed rule and status.
func (h *lifecycleHarness) grabWith(t *testing.T, title, hash, status string, enabled bool, ratio float64, hours int) int64 {
	t.Helper()
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash, seed_enabled, seed_ratio, seed_hours, status)
		VALUES (?, ?, 'Fake', 60, 'series', ?, ?, ?, ?, ?)`, h.seriesID, title, hash, boolToInt(enabled), ratio, hours, status)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (h *lifecycleHarness) setGlobal(d SeedDefault) {
	h.c.SetSeedDefault(func(context.Context) SeedDefault { return d })
}

// An imported torrent whose indexer set no goal seeded forever. With a global goal it is
// removed at that goal, with its data (the library has its own copy), and the show's
// history says the global goal ended it.
func TestGlobalSeedGoalEndsATorrentWhoseIndexerSetNone(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	gid := h.grabWith(t, name, hash, grabStatusImported, true, 0, 0)
	h.seedTorrent(hash, name, 3, 10) // ratio 3, 10 hours

	// Unset: exactly as before — no goal, never removed.
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("removed with no global goal set: %+v", *h.removed)
	}

	h.setGlobal(SeedDefault{Ratio: 2})
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 1 || (*h.removed)[0] != (removeCall{hash, true}) {
		t.Fatalf("removals = %+v, want one Remove(hash, with data)", *h.removed)
	}
	if s := grabStatus(t, h.c, gid); s != grabStatusSeeded {
		t.Errorf("grab after seeding = %q, want seeded", s)
	}
	if !hasEvent(h.seriesEvents(t), "seeded", "global goal of 2") {
		t.Errorf("the history doesn't say the global goal ended the seed: %+v", h.seriesEvents(t))
	}
}

// The indexer's own goal wins: a global ratio the torrent has passed doesn't remove it
// while its indexer's higher ratio isn't met.
func TestIndexerSeedGoalBeatsTheGlobalOne(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	h.grabWith(t, name, hash, grabStatusImported, true, 10, 0)
	h.seedTorrent(hash, name, 3, 1000)
	h.setGlobal(SeedDefault{Ratio: 1, Hours: 24})
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("removed at the global goal although the indexer wants ratio 10: %+v", *h.removed)
	}
}

// The global goal never reaches a torrent that isn't imported yet, one the user took over
// by dismissing its review, or one Arrmada didn't grab at all.
func TestGlobalSeedGoalLeavesWhatIsntOursToRemove(t *testing.T) {
	h := newLifecycleHarness(t)
	h.setGlobal(SeedDefault{Ratio: 0.5, Hours: 1})

	pending := "Show.S01.1080p.WEB-DL-AAA"
	h.grabWith(t, pending, hashFor(pending), grabStatusGrabbed, true, 0, 0)
	h.seedTorrent(hashFor(pending), pending, 5, 500)

	dismissed := "Show.S01.1080p.WEB-DL-BBB"
	h.grabWith(t, dismissed, hashFor(dismissed), grabStatusDismissed, true, 0, 0)
	h.seedTorrent(hashFor(dismissed), dismissed, 5, 500)

	manual := "Some.Other.Thing.2020.1080p-CCC"
	h.seedTorrent(hashFor(manual), manual, 5, 500) // in the client, no grab

	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("the global goal removed a torrent it must leave alone: %+v", *h.removed)
	}

	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Name] = r.Outcome
	}
	want := map[string]string{pending: SeedOutcomeNotYet, dismissed: SeedOutcomeForever, manual: SeedOutcomeNotOurs}
	for n, o := range want {
		if got[n] != o {
			t.Errorf("seed check for %s = %q, want %q", n, got[n], o)
		}
	}
}

// The seed check says what the next pass will do, by the same rules, and changes nothing;
// a value passed in stands in for the saved global goal.
func TestSeedCheckReportsWhatWouldHappen(t *testing.T) {
	h := newLifecycleHarness(t)
	met := "Show.S01.1080p.WEB-DL-AAA"
	h.grabWith(t, met, hashFor(met), grabStatusImported, true, 0, 0)
	h.seedTorrent(hashFor(met), met, 3, 10)
	short := "Show.S01.1080p.WEB-DL-BBB"
	h.grabWith(t, short, hashFor(short), grabStatusImported, true, 5, 0)
	h.seedTorrent(hashFor(short), short, 3, 10)
	off := "Show.S01.1080p.WEB-DL-CCC"
	h.grabWith(t, off, hashFor(off), grabStatusImported, false, 0, 0)
	h.seedTorrent(hashFor(off), off, 0, 1)

	// No global goal saved: the blank indexer's torrent seeds forever.
	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := func(rows []SeedCheckRow) map[string]SeedCheckRow {
		m := map[string]SeedCheckRow{}
		for _, r := range rows {
			m[r.Name] = r
		}
		return m
	}
	m := byName(rows)
	if m[met].Outcome != SeedOutcomeForever || m[short].Outcome != SeedOutcomeSeeding || m[off].Outcome != SeedOutcomeRemove {
		t.Errorf("unsaved: outcomes = %q / %q / %q", m[met].Outcome, m[short].Outcome, m[off].Outcome)
	}

	// Previewing a global ratio of 2: the blank indexer's torrent would go, by the global goal.
	rows, err = h.c.SeedCheckQueue(h.ctx, &SeedDefault{Ratio: 2})
	if err != nil {
		t.Fatal(err)
	}
	m = byName(rows)
	r := m[met]
	if r.Outcome != SeedOutcomeRemove || r.Goal.Source != SeedSourceGlobal || !strings.Contains(r.Reason, "global") {
		t.Errorf("preview: %+v, want removal by the global goal", r)
	}
	if m[short].Goal.Source != SeedSourceIndexer || m[short].Outcome != SeedOutcomeSeeding {
		t.Errorf("preview: the indexer's own goal should still apply: %+v", m[short])
	}
	if len(*h.removed) != 0 {
		t.Errorf("the seed check removed something: %+v", *h.removed)
	}
}

// A torrent added by hand (indexer "manual", which no indexer is called) used to get the
// standard window; with a global goal it gets that instead. A known indexer's own values
// are recorded as they are.
func TestSeedRulesFallBackToTheGlobalGoal(t *testing.T) {
	h := newLifecycleHarness(t)
	if en, r, hrs := h.c.seedRules(h.ctx, "manual"); !en || r != 0 || hrs != indexer.DefaultSeedHours {
		t.Errorf("no global goal: manual grab = %v/%v/%v, want the standard window", en, r, hrs)
	}
	h.setGlobal(SeedDefault{Ratio: 1.5, Hours: 48})
	if en, r, hrs := h.c.seedRules(h.ctx, "manual"); !en || r != 1.5 || hrs != 48 {
		t.Errorf("global goal: manual grab = %v/%v/%v, want 1.5/48", en, r, hrs)
	}
	// "Fake" exists (seeding off, as the harness made it): its own values, not the global.
	if en, r, hrs := h.c.seedRules(h.ctx, "Fake"); en || r != 0 || hrs != 0 {
		t.Errorf("known indexer = %v/%v/%v, want its own values", en, r, hrs)
	}
	// A usenet download never seeds, global goal or not.
	if _, en, _, _ := h.c.grabPolicy(h.ctx, "manual", "sab-nzo_1"); en {
		t.Error("a usenet grab was given a seed goal")
	}
}
