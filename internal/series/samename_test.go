package series

import (
	"strings"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// sameNameShow builds a show whose seasons have n episodes each, airing weekly from the
// season's first air date.
func sameNameShow(id int64, title string, year int, seasons map[int]string, n int) Series {
	s := Series{ID: id, Title: title, Year: year}
	for sn := 1; sn <= 20; sn++ {
		first, ok := seasons[sn]
		if !ok {
			continue
		}
		start, _ := time.Parse("2006-01-02", first)
		season := Season{SeasonNumber: sn}
		for e := 1; e <= n; e++ {
			season.Episodes = append(season.Episodes, Episode{
				SeasonNumber: sn, EpisodeNumber: e,
				AirDate: start.AddDate(0, 0, 7*(e-1)).Format("2006-01-02"),
			})
		}
		s.Seasons = append(s.Seasons, season)
	}
	return s
}

func scrubsPair() (old, revival Series) {
	old = sameNameShow(1, "Scrubs", 2001, map[int]string{
		1: "2001-10-02", 2: "2002-09-26", 3: "2003-10-02", 4: "2004-08-31", 5: "2006-01-03",
		6: "2006-11-30", 7: "2007-10-25", 8: "2009-01-06", 9: "2009-12-01",
	}, 20)
	revival = sameNameShow(2, "Scrubs", 2026, map[int]string{1: "2025-02-25", 2: "2026-03-03"}, 10)
	return old, revival
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestJudgeSameName(t *testing.T) {
	old, rev := scrubsPair()
	cases := []struct {
		name      string
		release   string
		show      Series
		published time.Time
		exact     bool
		want      SameName
		whyHas    string
	}{
		// The reporter's case: a yearless S02E03 for the 2001 show.
		{"old show, posted long before the revival's S02E03", "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", old, day("2015-06-01"), true, SameNameThis, ""},
		{"old show, posted after the revival's S02E03 aired", "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", old, day("2026-04-01"), true, SameNameUnsure, "Scrubs (2001) and Scrubs (2026) share a name"},
		{"old show, no publish date", "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", old, time.Time{}, true, SameNameUnsure, "doesn't say which"},
		{"revival, posted before its S02E03 aired", "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", rev, day("2015-06-01"), true, SameNameOther, "can't be this show's"},
		{"revival, posted after both aired", "Scrubs.S02E03.2160p.WEB-DL.x265-GRP", rev, day("2026-04-01"), true, SameNameUnsure, ""},
		{"revival, no publish date", "Scrubs.S02E03.1080p.WEB-DL-GRP", rev, time.Time{}, true, SameNameUnsure, ""},
		// Within the margin before the revival's air date still could be the revival's.
		{"old show, posted a week before the revival's episode", "Scrubs.S02E03.1080p.WEB-DL-GRP", old, day("2026-03-10"), true, SameNameUnsure, ""},
		// A season the revival doesn't list: ruled out only by the revival's premiere, since
		// its listing may just not be refreshed yet (#81 review).
		{"old show, a season the revival doesn't list, posted before it premiered", "Scrubs.S05E01.720p.HDTV-GRP", old, day("2015-01-01"), true, SameNameThis, ""},
		{"old show, a season the revival doesn't list, posted after it premiered", "Scrubs.S05E01.720p.HDTV-GRP", old, day("2026-06-01"), true, SameNameUnsure, ""},
		{"old show, a season the revival doesn't list, no date", "Scrubs.S05E01.720p.HDTV-GRP", old, time.Time{}, true, SameNameUnsure, ""},
		// Seasons, not episodes: S02E12 isn't in the revival's listing, but its season 2 is,
		// and it began in March 2026 — a release posted in mid-2025 can't be from it.
		{"old show, an episode past the revival's listing, posted before its season began", "Scrubs.S02E12.720p.HDTV-GRP", old, day("2025-06-01"), true, SameNameThis, ""},
		// Within the slack before the revival's season 2 began, though long before its E08
		// aired: numbering may differ between listings, so only the season's start counts.
		{"old show, S02E08 posted weeks before the revival's season 2 began", "Scrubs.S02E08.720p.HDTV-GRP", old, day("2026-02-10"), true, SameNameUnsure, ""},
		{"old show, added (not posted) before the revival premiered", "Scrubs.S05E01.720p.HDTV-GRP", old, day("2020-01-01"), false, SameNameThis, ""},
		// A multi-season pack: the revival lists S01–S02, not S03.
		{"old show, S01-S03 pack posted before the revival premiered", "Scrubs.S01-S03.1080p.BluRay-GRP", old, day("2015-01-01"), true, SameNameThis, ""},
		{"old show, S01-S03 pack posted after the revival premiered", "Scrubs.S01-S03.1080p.BluRay-GRP", old, day("2026-04-01"), true, SameNameUnsure, ""},
		{"revival, a season only the old show has", "Scrubs.S05E01.720p.HDTV-GRP", rev, day("2026-04-01"), true, SameNameUnsure, ""},
		// Season packs.
		{"old show, an old season pack", "Scrubs.S02.1080p.BluRay.x264-GRP", old, day("2012-01-01"), true, SameNameThis, ""},
		{"revival, its season pack posted after airing", "Scrubs.S01.1080p.WEB-DL-GRP", rev, day("2026-05-01"), true, SameNameUnsure, ""},
		{"old show, the whole show", "Scrubs.Complete.Series.S01-S09.1080p.BluRay-GRP", old, day("2012-01-01"), true, SameNameThis, ""},
		// The year names the show: nothing to decide.
		{"revival by its year", "Scrubs.2026.S02E03.1080p.WEB-DL-GRP", rev, time.Time{}, true, SameNameNone, ""},
		{"old show by its year", "Scrubs.2001.S02E03.1080p.WEB-DL-GRP", old, time.Time{}, true, SameNameNone, ""},
		// Only when the download was added is known: an upper bound.
		{"old show, added before the revival's episode", "Scrubs.S02E03.1080p.WEB-DL-GRP", old, day("2020-01-01"), false, SameNameThis, ""},
		{"old show, added after the revival's episode", "Scrubs.S02E03.1080p.WEB-DL-GRP", old, day("2026-05-01"), false, SameNameUnsure, ""},
		{"revival, added early: never rules the show itself out", "Scrubs.S02E03.1080p.WEB-DL-GRP", rev, day("2015-01-01"), false, SameNameUnsure, ""},
		// Numbering that can't be read as seasons and episodes.
		{"absolute numbering", "[Grp] Scrubs - 05 [1080p]", old, day("2012-01-01"), true, SameNameUnsure, ""},
	}
	named := func(s Series) []Series {
		if s.ID == old.ID {
			return []Series{rev}
		}
		return []Series{old}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parser.Parse(tc.release)
			got, why := JudgeSameName(p, tc.show, named(tc.show), tc.published, tc.exact)
			if got != tc.want {
				t.Fatalf("JudgeSameName(%q) = %v (%q), want %v", tc.release, got, why, tc.want)
			}
			if tc.whyHas != "" && !strings.Contains(why, tc.whyHas) {
				t.Errorf("why = %q, want it to mention %q", why, tc.whyHas)
			}
			if (got == SameNameUnsure || got == SameNameOther) && why == "" {
				t.Error("a refusal must say why")
			}
		})
	}
}

// A publish (or added) date from before 1990 or more than a day ahead is no date at all:
// indexers send the Unix epoch when they don't know, and a reset client clock does too.
func TestJudgeSameNameImplausibleDates(t *testing.T) {
	old, rev := scrubsPair()
	now := time.Now()
	for _, pub := range []time.Time{time.Unix(0, 0).UTC(), day("1989-12-31"), now.Add(48 * time.Hour), day("2099-01-01")} {
		for _, exact := range []bool{true, false} {
			// Taken at face value, each would rule the revival out (or in) by date.
			if got, _ := JudgeSameName(parser.Parse("Scrubs.S02E03.1080p.WEB-DL-GRP"), old, []Series{rev}, pub, exact); got != SameNameUnsure {
				t.Errorf("old show, published %v (exact %v) = %v, want unsure", pub, exact, got)
			}
		}
		if got, _ := JudgeSameName(parser.Parse("Scrubs.S02E03.1080p.WEB-DL-GRP"), rev, []Series{old}, pub, true); got != SameNameUnsure {
			t.Errorf("revival, published %v = %v, want unsure (never Other on a bogus date)", pub, got)
		}
	}
	// The edges: 1990-01-01 and a day ahead are believed — 1990 is long before the 2001
	// show's season 2, so taken at face value it can't be that show's.
	if got, _ := JudgeSameName(parser.Parse("Scrubs.S02E03.1080p.WEB-DL-GRP"), old, []Series{rev}, day("1990-01-01"), true); got != SameNameOther {
		t.Errorf("1990-01-01 = %v, want other (the date believed)", got)
	}
	if !plausibleDate(now.Add(23*time.Hour), now) || plausibleDate(now.Add(25*time.Hour), now) || plausibleDate(time.Time{}, now) {
		t.Error("plausibleDate's window is off")
	}
}

// Specials are never dated by the sibling's season 0: TVDB and TMDB disagree about it.
// The sibling's whole-show premiere is used instead.
func TestJudgeSameNameSpecials(t *testing.T) {
	old, rev := scrubsPair()
	// The revival's "specials" dated 2030 would rule out anything posted before; its
	// premiere (2025) is what counts.
	rev.Seasons = append(rev.Seasons, Season{SeasonNumber: 0, Episodes: []Episode{{SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2030-01-01"}}})
	if got, _ := JudgeSameName(parser.Parse("Scrubs.S00E01.1080p.WEB-DL-GRP"), old, []Series{rev}, day("2025-06-01"), true); got != SameNameUnsure {
		t.Errorf("S00E01 posted after the revival premiered = %v, want unsure", got)
	}
	if got, _ := JudgeSameName(parser.Parse("Scrubs.S00E01.1080p.WEB-DL-GRP"), old, []Series{rev}, day("2015-06-01"), true); got != SameNameThis {
		t.Errorf("S00E01 posted before the revival premiered = %v, want this", got)
	}
	// Nor is the show's own season 0 proof it can't be this show's.
	old.Seasons = append(old.Seasons, Season{SeasonNumber: 0, Episodes: []Episode{{SeasonNumber: 0, EpisodeNumber: 1, AirDate: "2030-01-01"}}})
	if got, _ := JudgeSameName(parser.Parse("Scrubs.S00E01.1080p.WEB-DL-GRP"), old, []Series{rev}, day("2015-06-01"), true); got == SameNameOther {
		t.Error("the show's own season 0 date ruled the release out")
	}
	if _, complete, known := seasonsPremiere(parser.Parse("Scrubs.S00E01.1080p.WEB-DL-GRP"), rev); complete || !known {
		t.Errorf("season 0 read as covered (complete=%v known=%v)", complete, known)
	}
}

// A sibling with no seasons or air dates yet (just added) can't be ruled out by any date.
func TestJudgeSameNameBareSibling(t *testing.T) {
	old, _ := scrubsPair()
	bare := Series{ID: 2, Title: "Scrubs", Year: 2026}
	noDates := sameNameShow(2, "Scrubs", 2026, map[int]string{1: "2025-02-25"}, 3)
	for i := range noDates.Seasons[0].Episodes {
		noDates.Seasons[0].Episodes[i].AirDate = ""
	}
	for _, sib := range []Series{bare, noDates} {
		for _, rel := range []string{"Scrubs.S02E03.1080p.WEB-DL-GRP", "Scrubs.S01E01.1080p.WEB-DL-GRP", "Scrubs.S02.1080p.BluRay-GRP"} {
			if got, _ := JudgeSameName(parser.Parse(rel), old, []Series{sib}, day("2005-01-01"), true); got != SameNameUnsure {
				t.Errorf("%q against a sibling with %d seasons and no dates = %v, want unsure", rel, len(sib.Seasons), got)
			}
		}
	}
}

// A release is dated against its seasons' premieres; a pack is covered only when the show
// lists every season in it.
func TestSeasonsPremiere(t *testing.T) {
	_, rev := scrubsPair()
	if _, complete, known := seasonsPremiere(parser.Parse("Scrubs.S01-S03.1080p.BluRay-GRP"), rev); complete || !known {
		t.Errorf("S01-S03 against a show listing S01-S02: complete=%v known=%v, want incomplete", complete, known)
	}
	if first, complete, _ := seasonsPremiere(parser.Parse("Scrubs.S01-S02.1080p.BluRay-GRP"), rev); !complete || !first.Equal(day("2025-02-25")) {
		t.Errorf("S01-S02: first %v complete=%v, want the season 1 premiere", first, complete)
	}
	// Any episode of season 2, listed or not, is dated by the season's premiere.
	for _, rel := range []string{"Scrubs.S02E01.1080p.WEB-DL-GRP", "Scrubs.S02E08.1080p.WEB-DL-GRP", "Scrubs.S02E30.1080p.WEB-DL-GRP"} {
		if first, complete, _ := seasonsPremiere(parser.Parse(rel), rev); !complete || !first.Equal(day("2026-03-03")) {
			t.Errorf("%s: first %v complete=%v, want season 2's premiere", rel, first, complete)
		}
	}
	if _, _, known := seasonsPremiere(parser.Parse("[Grp] Scrubs - 05 [1080p]"), rev); known {
		t.Error("absolute numbering read as seasons")
	}
}

// A show without a same-named library show is never judged: every release is
// SameNameNone, whatever its date.
func TestJudgeSameNameNoSibling(t *testing.T) {
	old, _ := scrubsPair()
	other := sameNameShow(3, "House", 2004, map[int]string{1: "2004-11-16"}, 22)
	for _, rel := range []string{"Scrubs.S02E03.2160p.WEB-DL.x265-GRP", "Scrubs.S02.1080p.BluRay-GRP", "[Grp] Scrubs - 05"} {
		for _, pub := range []time.Time{{}, day("1990-01-01"), day("2030-01-01")} {
			if got, why := JudgeSameName(parser.Parse(rel), old, nil, pub, true); got != SameNameNone || why != "" {
				t.Errorf("no siblings: %q at %v = %v %q, want none", rel, pub, got, why)
			}
			if got, _ := JudgeSameName(parser.Parse(rel), old, []Series{other}, pub, true); got != SameNameNone {
				t.Errorf("a differently named show: %q = %v, want none", rel, got)
			}
		}
	}
}

func TestSameNamed(t *testing.T) {
	old, rev := scrubsPair()
	house := Series{ID: 3, Title: "House"}
	officeUS := Series{ID: 4, Title: "The Office", Year: 2005, Extra: &SeriesExtra{OriginCountry: []string{"US"}}}
	officeUK := Series{ID: 5, Title: "The Office", Year: 2001, Extra: &SeriesExtra{OriginCountry: []string{"GB"}}}
	all := []Series{old, rev, house, officeUS, officeUK}
	ids := func(ss []Series) []int64 {
		var out []int64
		for _, s := range ss {
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids(SameNamed(old, all)); len(got) != 1 || got[0] != 2 {
		t.Errorf("SameNamed(Scrubs 2001) = %v, want [2]", got)
	}
	if got := SameNamed(house, all); len(got) != 0 {
		t.Errorf("SameNamed(House) = %v, want none", ids(got))
	}
	if got := ids(SameNamed(officeUK, all)); len(got) != 1 || got[0] != 4 {
		t.Errorf("SameNamed(The Office UK) = %v, want [4]", got)
	}
	// A country tag names the show, as a year does.
	if sib := SameNameSiblings(parser.Parse("The.Office.US.S01E01.720p.WEB-DL-GRP"), officeUS, []Series{officeUK}); len(sib) != 0 {
		t.Errorf("a US-tagged release has siblings %v, want none", ids(sib))
	}
	if sib := SameNameSiblings(parser.Parse("The.Office.S01E01.720p.WEB-DL-GRP"), officeUS, []Series{officeUK}); len(sib) != 1 {
		t.Errorf("an untagged release has siblings %v, want the UK show", ids(sib))
	}
}
