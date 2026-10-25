package automation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
)

// The goal a torrent seeds to: what was recorded on its grab, or — only for a grab
// recorded on the global goal — the global goal, read live. A grab from before the global
// goal existed, and a 0/0 ("seed forever") grab, never pick it up.
func TestEffectiveSeedGoalPrecedence(t *testing.T) {
	global := SeedDefault{Ratio: 1, Hours: 72}
	g := func(source string, enabled bool, ratio float64, hours int) grab {
		return grab{SeedEnabled: enabled, SeedRatio: ratio, SeedHours: hours, SeedSource: source, Status: grabStatusImported}
	}
	cases := []struct {
		name string
		g    grab
		def  SeedDefault
		want SeedPolicy
	}{
		{"an own goal stays its own", g(seedRecOwn, true, 2, 0), global, SeedPolicy{Enabled: true, Ratio: 2, Source: SeedSourceIndexer}},
		{"seed forever stays forever with a global goal set", g(seedRecNone, true, 0, 0), global, SeedPolicy{Enabled: true, Source: SeedSourceNone}},
		{"a grab from before the global goal (0/0) stays forever", g("", true, 0, 0), global, SeedPolicy{Enabled: true, Source: SeedSourceNone}},
		{"a grab from before the global goal keeps its recorded goal", g("", true, 0, 500), global, SeedPolicy{Enabled: true, Hours: 500, Source: SeedSourceIndexer}},
		{"a grab on the global goal follows it", g(seedRecGlobal, true, 0, 0), global, SeedPolicy{Enabled: true, Ratio: 1, Hours: 72, Source: SeedSourceGlobal}},
		{"a grab on the global goal with none set gets the standard window", g(seedRecGlobal, true, 0, 0), SeedDefault{}, SeedPolicy{Enabled: true, Hours: indexer.DefaultSeedHours, Source: SeedSourceGlobal, GlobalUnset: true}},
		{"a manual grab's standard window says so", g(seedRecDefault, true, 0, 336), global, SeedPolicy{Enabled: true, Hours: 336, Source: SeedSourceDefault}},
		{"seeding off stays off", g(seedRecOwn, false, 0, 0), global, SeedPolicy{Source: SeedSourceOff}},
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

// grabRule is the seed rule a test grab is recorded with.
type grabRule struct {
	source  string
	enabled bool
	ratio   float64
	hours   int
}

// grabWith records a series grab with the given seed rule and status.
func (h *lifecycleHarness) grabWith(t *testing.T, title, hash, status string, r grabRule) int64 {
	t.Helper()
	res, err := h.c.db.Exec(`INSERT INTO grabs (movie_id, title, indexer, stall_minutes, media_type, info_hash, seed_enabled, seed_ratio, seed_hours, seed_source, status)
		VALUES (?, ?, 'Fake', 60, 'series', ?, ?, ?, ?, ?, ?)`, h.seriesID, title, hash, boolToInt(r.enabled), r.ratio, r.hours, r.source, status)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (h *lifecycleHarness) setGlobal(d SeedDefault) {
	h.c.SetSeedDefault(func(context.Context) SeedDefault { return d })
}

var (
	onGlobal = grabRule{source: seedRecGlobal, enabled: true}
	forever  = grabRule{source: seedRecNone, enabled: true}
)

// Setting a global goal never reaches what was set to seed forever, or a grab from before
// the global goal existed: their owner chose "forever", and the global goal used to take
// that over and delete them with their data.
func TestGlobalSeedGoalIsNeverRetroactive(t *testing.T) {
	h := newLifecycleHarness(t)
	h.setGlobal(SeedDefault{Ratio: 2})
	chosen := "Show.S01.1080p.WEB-DL-AAA"
	h.grabWith(t, chosen, hashFor(chosen), grabStatusImported, forever)
	h.seedTorrent(hashFor(chosen), chosen, 5, 1000)
	legacy := "Show.S01.1080p.WEB-DL-BBB"
	h.grabWith(t, legacy, hashFor(legacy), grabStatusImported, grabRule{enabled: true}) // seed_source ''
	h.seedTorrent(hashFor(legacy), legacy, 5, 1000)

	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("the global goal removed a torrent set to seed forever: %+v", *h.removed)
	}
}

// A grab on the global goal is removed at it, with its data, and the show's history says
// the global goal ended the seed. The goal is read live: clearing it falls back to the
// standard window rather than "forever" or the old value.
func TestGlobalSeedGoalEndsAGrabRecordedOnIt(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	gid := h.grabWith(t, name, hash, grabStatusImported, onGlobal)
	h.seedTorrent(hash, name, 3, 10) // ratio 3, 10 hours

	h.setGlobal(SeedDefault{}) // none set: the standard 14 days, not met at 10 hours
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("removed before the standard window with no global goal set: %+v", *h.removed)
	}
	h.setGlobal(SeedDefault{Ratio: 4}) // raised past what it has
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("removed short of the global ratio: %+v", *h.removed)
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

// A grab that knows its info hash matches only that torrent. Another torrent with the
// same name — a cross-seed, a copy added by hand — used to match it by name and be
// removed with its data.
func TestSeedCleanupNeverMatchesAnotherHashByName(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	h.grabWith(t, name, hashFor(name), grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 1})
	other := hashFor("someone else's copy")
	h.seedTorrent(other, name, 5, 1000)

	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("a same-name torrent with another hash was removed: %+v", *h.removed)
	}
	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outcome != SeedOutcomeNotOurs {
		t.Errorf("seed check = %+v, want it reported as not ours", rows)
	}
}

// A grab without a hash (from before hashes were kept) can only be told by its name. Two
// torrents of that name used to both be removed by it in one pass; now a name that two
// downloads share matches neither, and a grab removes one torrent at most.
func TestSeedCleanupRemovesOneTorrentPerGrab(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	h.grabWith(t, name, "", grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 1})
	h.seedTorrent(hashFor("copy one"), name, 5, 1000)
	h.seedTorrent(hashFor("copy two"), name+".mkv", 5, 1000) // the same name once normalized

	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("one hashless grab removed %d torrents: %+v", len(*h.removed), *h.removed)
	}
}

// No more than ten torrents go in one pass, whatever ends them: an indexer's own goal,
// seeding off (removed once imported) or the global goal. The rest go on the next pass.
func TestSeedCleanupIsCappedPerPass(t *testing.T) {
	const limit, total = 10, 13
	h := newLifecycleHarness(t)
	h.setGlobal(SeedDefault{Hours: 1})
	rules := []grabRule{{source: seedRecOwn, enabled: true, ratio: 1}, {source: seedRecOwn}, onGlobal}
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("Show.S01E%02d.1080p.WEB-DL-GRP", i+1)
		h.grabWith(t, name, hashFor(name), grabStatusImported, rules[i%len(rules)])
		h.seedTorrent(hashFor(name), name, 5, 1000)
	}
	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	later := 0
	for _, r := range rows {
		if r.Later {
			later++
		}
	}
	if later != total-limit {
		t.Errorf("seed check marks %d removals for a later pass, want %d", later, total-limit)
	}
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != limit {
		t.Fatalf("one pass removed %d, want %d", len(*h.removed), limit)
	}
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != total {
		t.Fatalf("after the next pass %d removed, want all %d", len(*h.removed), total)
	}
}

// A series import that left files unmatched leaves them only in the download; removing it
// with its data would delete them, so it keeps seeding past its goal.
func TestSeedCleanupKeepsATorrentWithUnmatchedFiles(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	hash := hashFor(name)
	h.grabWith(t, name, hash, grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 1})
	h.c.noteImportUnmatched(h.ctx, hash, name, 2)
	h.seedTorrent(hash, name, 5, 1000)

	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("removed with its data although its import left files unmatched: %+v", *h.removed)
	}
	rows, _ := h.c.SeedCheckQueue(h.ctx, nil)
	if len(rows) != 1 || rows[0].Outcome != SeedOutcomeUnmatched {
		t.Errorf("seed check = %+v, want kept_unmatched", rows)
	}
	// A later import that matched everything it looked at (one file imported by hand)
	// doesn't clear it: the files it didn't look at are still only in the download.
	h.c.noteImportUnmatched(h.ctx, hash, name, 0)
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 0 {
		t.Fatalf("a later import cleared the unmatched mark and the torrent was removed: %+v", *h.removed)
	}
}

// The global goal never reaches a torrent that isn't imported yet, or one Arrmada didn't
// grab at all.
func TestGlobalSeedGoalLeavesWhatIsntOursToRemove(t *testing.T) {
	h := newLifecycleHarness(t)
	h.setGlobal(SeedDefault{Ratio: 0.5, Hours: 1})

	pending := "Show.S01.1080p.WEB-DL-AAA"
	h.grabWith(t, pending, hashFor(pending), grabStatusGrabbed, onGlobal)
	h.seedTorrent(hashFor(pending), pending, 5, 500)

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
	want := map[string]string{pending: SeedOutcomeNotYet, manual: SeedOutcomeNotOurs}
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
	h.grabWith(t, met, hashFor(met), grabStatusImported, onGlobal)
	h.seedTorrent(hashFor(met), met, 3, 10)
	short := "Show.S01.1080p.WEB-DL-BBB"
	h.grabWith(t, short, hashFor(short), grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 5})
	h.seedTorrent(hashFor(short), short, 3, 10)
	off := "Show.S01.1080p.WEB-DL-CCC"
	h.grabWith(t, off, hashFor(off), grabStatusImported, grabRule{source: seedRecOwn})
	h.seedTorrent(hashFor(off), off, 0, 1)

	byName := func(rows []SeedCheckRow) map[string]SeedCheckRow {
		m := map[string]SeedCheckRow{}
		for _, r := range rows {
			m[r.Name] = r
		}
		return m
	}
	// No global goal saved: the grab on it gets the standard window, not met at 10 hours.
	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(rows)
	if m[met].Outcome != SeedOutcomeSeeding || !m[met].Goal.GlobalUnset || m[short].Outcome != SeedOutcomeSeeding || m[off].Outcome != SeedOutcomeRemove {
		t.Errorf("unsaved: %+v / %+v / %+v", m[met], m[short], m[off])
	}

	// Previewing a global ratio of 2: the grab on the global goal would go, by it.
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

// What a new grab records: a manual grab gets the standard window, or the global goal
// (live) while one is set; a known indexer's own values are recorded as they are, and
// only an indexer switched to "use the global default" is recorded on it.
func TestSeedRulesRecordWhereTheGoalCameFrom(t *testing.T) {
	h := newLifecycleHarness(t)
	type rule struct {
		en    bool
		r     float64
		hrs   int
		where string
	}
	rules := func(name string) rule {
		en, r, hrs, where := h.c.seedRules(h.ctx, name)
		return rule{en, r, hrs, where}
	}
	if got := rules("manual"); got != (rule{true, 0, indexer.DefaultSeedHours, seedRecDefault}) {
		t.Errorf("no global goal: manual grab = %+v, want the standard window", got)
	}
	// A ratio-only global goal could end a hand-added private torrent within hours: such
	// a grab keeps the standard window.
	h.setGlobal(SeedDefault{Ratio: 1})
	if got := rules("manual"); got != (rule{true, 0, indexer.DefaultSeedHours, seedRecDefault}) {
		t.Errorf("ratio-only global goal: manual grab = %+v, want the standard window", got)
	}
	h.setGlobal(SeedDefault{Ratio: 1.5, Hours: 48})
	if got := rules("manual"); got != (rule{true, 0, 0, seedRecGlobal}) {
		t.Errorf("global goal with a time: manual grab = %+v, want it on the global goal", got)
	}
	// "Fake" exists (seeding off, as the harness made it): its own values.
	if got := rules("Fake"); got != (rule{false, 0, 0, seedRecOwn}) {
		t.Errorf("seeding-off indexer = %+v", got)
	}
	mk := func(name string, ix indexer.Indexer) {
		ix.Name, ix.Kind, ix.URL, ix.Enabled = name, indexer.KindTorznab, "http://"+name, true
		if _, err := h.c.indexers.Create(h.ctx, ix); err != nil {
			t.Fatal(err)
		}
	}
	mk("Forever", indexer.Indexer{SeedEnabled: true})
	mk("Own", indexer.Indexer{SeedEnabled: true, SeedRatio: 3})
	mk("OnGlobal", indexer.Indexer{SeedEnabled: true, SeedUseGlobal: true})
	if got := rules("Forever"); got != (rule{true, 0, 0, seedRecNone}) {
		t.Errorf("0/0 indexer = %+v, want seed forever, not the global goal", got)
	}
	if got := rules("Own"); got != (rule{true, 3, 0, seedRecOwn}) {
		t.Errorf("own-goal indexer = %+v", got)
	}
	if got := rules("OnGlobal"); got != (rule{true, 0, 0, seedRecGlobal}) {
		t.Errorf("indexer on the global goal = %+v", got)
	}
	// A usenet download never seeds, global goal or not.
	if _, en, _, _, _ := h.c.grabPolicy(h.ctx, "manual", "sab-nzo_1"); en {
		t.Error("a usenet grab was given a seed goal")
	}
}

// The matcher lets a grab act once per pass: a torrent listed twice (the same hash in two
// clients) can't be removed twice by one grab.
func TestSeedMatcherLetsAGrabActOnce(t *testing.T) {
	g := []grab{{ID: 1, Title: "Show.S01", InfoHash: "abc"}}
	a := download.Item{Hash: "abc", Name: "Show.S01"}
	m := newSeedMatcher(g, []download.Item{a, a})
	first := m.match(a)
	if first == nil {
		t.Fatal("no match for the grab's own hash")
	}
	m.use(first)
	if again := m.match(a); again != nil {
		t.Fatalf("a used grab matched again: %+v", again)
	}
}

// End to end: one grab, its torrent listed twice. Only one removal.
func TestSeedCleanupRemovesOneTorrentForTwoListings(t *testing.T) {
	h := newLifecycleHarness(t)
	name := "Show.S01.1080p.WEB-DL-GRP"
	h.grabWith(t, name, hashFor(name), grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 1})
	h.seedTorrent(hashFor(name), name, 5, 1000)
	h.seedTorrent(hashFor(name), name, 5, 1000)
	queue, err := h.c.downloads.Queue(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 {
		t.Skipf("the client read folds the two listings into %d", len(queue))
	}
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 1 {
		t.Fatalf("one grab removed %d torrents: %+v", len(*h.removed), *h.removed)
	}
}

// unplaceablePack writes a season pack with one file for an episode the show doesn't
// have, and puts it in the client as a finished, well-seeded torrent.
func (h *lifecycleHarness) unplaceablePack(t *testing.T, name string) string {
	t.Helper()
	dir := h.download(t, name, "Show.S01E01.1080p.WEB-DL-GRP.mkv", "Show.S01E02.1080p.WEB-DL-GRP.mkv", "Show.S01E09.1080p.WEB-DL-GRP.mkv")
	defer h.c.downloads.InvalidateSnapshot()
	h.qbit.mu.Lock()
	defer h.qbit.mu.Unlock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hashFor(name), "name": name, "state": "uploading", "progress": 1.0, "amount_left": 0,
		"size": 3 * 60 << 20, "completed": 3 * 60 << 20, "downloaded": 1 << 30, "uploaded": 5 << 30,
		"seeding_time": 1000 * 3600, "category": seriesCategory, "content_path": dir,
	})
	return dir
}

func importUnmatchedOf(t *testing.T, c *Coordinator, id int64) int {
	t.Helper()
	var n int
	if err := c.db.QueryRow(`SELECT import_unmatched FROM grabs WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Every series import path records the files it couldn't place on the grab, so seed
// cleanup keeps the torrent rather than delete them: the import sweep, a review's Import
// anyway, and a manual folder import.
func TestEverySeriesImportRecordsUnmatchedFiles(t *testing.T) {
	name := "Show.S01.1080p.WEB-DL-GRP"
	check := func(t *testing.T, h *lifecycleHarness, gid int64) {
		t.Helper()
		if s := grabStatus(t, h.c, gid); s != grabStatusImported {
			t.Fatalf("grab after the import = %q, want imported", s)
		}
		if n := importUnmatchedOf(t, h.c, gid); n != 1 {
			t.Fatalf("import_unmatched = %d, want 1 (S01E09 has no episode)", n)
		}
		h.c.ManageSeeding(h.ctx)
		if len(*h.removed) != 0 {
			t.Fatalf("removed with its data although a file was left unplaced: %+v", *h.removed)
		}
	}
	t.Run("sweep", func(t *testing.T) {
		h := newLifecycleHarness(t)
		gid := h.seriesGrab(t, name, hashFor(name))
		h.unplaceablePack(t, name)
		h.c.ImportSeriesDownloads(h.ctx)
		check(t, h, gid)
	})
	t.Run("review import anyway", func(t *testing.T) {
		h := newLifecycleHarness(t)
		gid := h.seriesGrab(t, name, hashFor(name))
		dir := h.unplaceablePack(t, name)
		rid := h.hold(t, hashFor(name), name, dir)
		if err := h.c.ImportReview(h.ctx, rid, 0, ""); err != nil {
			t.Fatal(err)
		}
		check(t, h, gid)
	})
	t.Run("manual folder import", func(t *testing.T) {
		h := newLifecycleHarness(t)
		gid := h.seriesGrab(t, name, hashFor(name))
		dir := h.unplaceablePack(t, name)
		if err := h.c.ManualImportSeries(h.ctx, h.seriesID, dir); err != nil {
			t.Fatal(err)
		}
		check(t, h, gid)
	})
}

// The seed check pairs an older grab with its torrent the way the next cleanup pass will
// (AdoptTorrentHashes), so the preview matches what happens, and writes nothing doing so.
func TestSeedCheckPairsHashlessGrabsWithoutWriting(t *testing.T) {
	h := newLifecycleHarness(t)
	listing := "Show S01 1080p WEB-DL DD+ GRP" // the indexer's prettified listing title
	torrent := "Show.S01.1080p.WEB-DL.EAC3-GRP"
	gid := h.grabWith(t, listing, "", grabStatusImported, grabRule{source: seedRecOwn, enabled: true, ratio: 1})
	h.seedTorrent(hashFor(torrent), torrent, 5, 1000)

	rows, err := h.c.SeedCheckQueue(h.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outcome != SeedOutcomeRemove {
		t.Fatalf("seed check = %+v, want the paired grab's removal", rows)
	}
	var hash string
	if err := h.c.db.QueryRow(`SELECT info_hash FROM grabs WHERE id = ?`, gid).Scan(&hash); err != nil || hash != "" {
		t.Fatalf("the check wrote a hash on the grab: %q (err %v)", hash, err)
	}
	h.c.ManageSeeding(h.ctx)
	if len(*h.removed) != 1 {
		t.Fatalf("the cleanup pass did something other than what the check said: %+v", *h.removed)
	}
}
