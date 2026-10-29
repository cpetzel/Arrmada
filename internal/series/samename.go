package series

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// Same-named shows (#81). Two library shows with one title — Scrubs (2001) and its 2026
// revival, The Office UK and US — both fit a release that names neither its year nor its
// country: "Scrubs.S02E03.2160p…" is S02E03 of whichever. Import routing has always held
// such a download when it wasn't grabbed (MatchRelease returns the candidates), but a
// search grabbed it for the show it searched for, and the import trusted the grab — so the
// revival's season 2 could replace the original's S02E03.
//
// The release's publish date settles most of it: a release posted before a show's
// episode aired can't be that episode. Everything here only ever applies to a show that
// has a same-named sibling the release fits as well as it fits the show; for every other
// show it answers SameNameNone and nothing changes.

// SameName is how a release relates to a show that shares its name with another library
// show.
type SameName int

const (
	// SameNameNone: no other library show fits the release — the usual case, and the only
	// answer for a show without a same-named sibling. Nothing changes.
	SameNameNone SameName = iota
	// SameNameThis: other shows share the name, but the dates rule each of them out, so
	// the release can only be this show's.
	SameNameThis
	// SameNameOther: posted before this show's episodes aired, so it can't be this show's.
	SameNameOther
	// SameNameUnsure: it could be this show's or a sibling's, and nothing says which.
	SameNameUnsure
)

// SameNameMargin is how far before an episode's air date a release may be posted and still
// be that episode: air dates are days in one time zone, and leaks and early streaming
// drops come out ahead of them. Generous on purpose — it only ever makes a release more
// ambiguous, never less, and the shows it separates are years apart.
const SameNameMargin = 30 * 24 * time.Hour

// SameNamed are the library shows other than s that go by one of s's own names (its
// title, an anime's Latin original title), in any year or country. Only these can ever
// fit a release the way s does, so a show with none skips every check below. all needs
// only titles (a List or searchLibrary snapshot).
func SameNamed(s Series, all []Series) []Series {
	return SameNameIndex(all)(s)
}

// SameNameIndex indexes a library snapshot by its shows' own title keys once, for a pass
// that asks SameNamed about many shows (the import sweep).
func SameNameIndex(all []Series) func(s Series) []Series {
	byKey := map[string][]int{}
	for i, o := range all {
		seen := map[string]bool{}
		for _, t := range o.ownTitles() {
			if k := parser.TitleKey(t); k != "" && !seen[k] {
				seen[k] = true
				byKey[k] = append(byKey[k], i)
			}
		}
	}
	return func(s Series) []Series {
		var out []Series
		taken := map[int]bool{}
		for _, t := range s.ownTitles() {
			k := parser.TitleKey(t)
			if k == "" {
				continue
			}
			for _, i := range byKey[k] {
				if all[i].ID != s.ID && !taken[i] {
					taken[i] = true
					out = append(out, all[i])
				}
			}
		}
		return out
	}
}

// SameNameShows loads the library shows that share a name with s (SameNamed), each with
// its seasons, episodes and aliases — what JudgeSameName reads. nil, without loading any
// show, when none does.
func (svc *Service) SameNameShows(ctx context.Context, s Series) ([]Series, error) {
	all, err := svc.SameNameLibrary(ctx)
	if err != nil {
		return nil, err
	}
	return svc.LoadSameNamed(ctx, SameNamed(s, all))
}

// SameNameLibrary is the library snapshot SameNamed needs (titles, years, origin, aliases),
// for a pass that judges many shows and builds SameNameIndex over it once.
func (svc *Service) SameNameLibrary(ctx context.Context) ([]Series, error) {
	return svc.repo.searchLibrary(ctx)
}

// LoadSameNamed loads the shows in named (SameNamed) in full for JudgeSameName. Unlike
// Get, an episode list that can't be read is an error: a sibling read as having no
// episodes would look like one that can't own the release.
func (svc *Service) LoadSameNamed(ctx context.Context, named []Series) ([]Series, error) {
	if len(named) == 0 {
		return nil, nil
	}
	out := make([]Series, 0, len(named))
	for _, o := range named {
		full, err := svc.LoadForSameName(ctx, o.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, full)
	}
	return out, nil
}

// LoadForSameName is one show with its seasons, episodes and aliases, read strictly
// (LoadSameNamed).
func (svc *Service) LoadForSameName(ctx context.Context, id int64) (Series, error) {
	full, err := svc.repo.Get(ctx, id)
	if err != nil {
		return Series{}, err
	}
	seasons, err := svc.repo.seasonsFor(ctx, id, true)
	if err != nil {
		return Series{}, err
	}
	full.Seasons = seasons
	full.Aliases = svc.repo.Aliases(ctx, id)
	return full, nil
}

// SameNameSiblings are the shows among named (SameNameShows) that p fits exactly as well
// as it fits s: by their own title, with no year or country that picks s over them. Empty
// when p names its show (a year or country only s has), when it fits s only through an
// alias, or when it doesn't fit s at all — the rules that already decide those stay as
// they are.
func SameNameSiblings(p parser.Release, s Series, named []Series) []Series {
	if len(named) == 0 {
		return nil
	}
	fs := FitRelease(p, s)
	if !fs.OK || fs.Alias {
		return nil
	}
	sure := fs.Year || fs.Country
	var out []Series
	for _, o := range named {
		if o.ID == s.ID {
			continue
		}
		fo := FitRelease(p, o)
		if !fo.OK || fo.Alias {
			continue
		}
		if sure && !fo.Year && !fo.Country {
			continue // the release's year or country is s's and not this one's
		}
		out = append(out, o)
	}
	return out
}

// JudgeSameName decides whether release p (with the parsed name) can be s's when other
// library shows share s's name. named is SameNameShows(s); s and named carry their
// episodes.
//
// published is when the release was posted. exact says it is the indexer's publish date;
// when it isn't (an import that only knows when the download was added, which is never
// before the release was posted) it is used only as an upper bound: it can rule a
// sibling out, never s. A zero published rules nothing out.
//
// For each show, the episodes the release covers are read from the show's own episode
// list: a season and episode, a season pack, several seasons, or the whole show. A release
// posted more than SameNameMargin before the earliest of those episodes aired isn't that
// show's. A sibling that doesn't list all of them — metadata not refreshed since a season
// was announced, a show just added — is ruled out only by its premiere: a release posted
// more than SameNameMargin before the show's first episode aired. What can't be worked out
// — no season in the name (anime absolute numbering), no air dates at all — counts as
// could-be.
//
//   - no sibling fits p: SameNameNone, unchanged;
//   - published (exact) before s's episodes aired: SameNameOther;
//   - every sibling ruled out: SameNameThis;
//   - otherwise SameNameUnsure. why names the shows.
func JudgeSameName(p parser.Release, s Series, named []Series, published time.Time, exact bool) (SameName, string) {
	sibs := SameNameSiblings(p, s, named)
	if len(sibs) == 0 {
		return SameNameNone, ""
	}
	if exact && !published.IsZero() {
		if first, ok := firstAired(p, s); ok && published.Before(first.Add(-SameNameMargin)) {
			return SameNameOther, fmt.Sprintf("it was posted %s, before %s's %s aired (%s) — it can't be this show's, and %s shares its name",
				published.UTC().Format("2006-01-02"), labelOf(s), coverLabel(p), first.Format("2006-01-02"), DescribeCandidates(sibs))
		}
	}
	var could []Series
	for _, o := range sibs {
		if couldBe(p, o, published) {
			could = append(could, o)
		}
	}
	if len(could) == 0 {
		return SameNameThis, ""
	}
	return SameNameUnsure, fmt.Sprintf("%s share a name and this release doesn't say which",
		joinAnd(append([]string{labelOf(s)}, labelsOf(could)...)))
}

// couldBe reports whether the release can be o's. It can't when it was posted more than
// SameNameMargin before the earliest covered episode aired, when o lists every episode it
// covers; when o doesn't, only when it was posted that long before o's premiere.
func couldBe(p parser.Release, o Series, published time.Time) bool {
	eps, complete, known := coveredEpisodes(p, o)
	if !known || published.IsZero() {
		return true // the numbering can't be read against the show, or no date to go by
	}
	if !complete {
		eps = nil
		for _, sn := range o.Seasons {
			eps = append(eps, sn.Episodes...)
		}
	}
	first, ok := earliestAir(eps)
	if !ok {
		return true // no air dates (or no episodes at all): nothing rules it out
	}
	return !published.Before(first.Add(-SameNameMargin))
}

// firstAired is the earliest air date of the episodes of s the release covers; ok is false
// when there's none to go by.
func firstAired(p parser.Release, s Series) (time.Time, bool) {
	eps, _, known := coveredEpisodes(p, s)
	if !known || len(eps) == 0 {
		return time.Time{}, false
	}
	return earliestAir(eps)
}

// coveredEpisodes are the episodes of s that p names. complete is whether s lists every
// one of them: each episode of an episode release, each season of a pack, some regular
// episode for a whole-show pack. known is false when the release's numbering can't be read
// as seasons and episodes (no season in the name: anime absolute numbering, a date-named
// episode).
func coveredEpisodes(p parser.Release, s Series) (eps []Episode, complete, known bool) {
	switch p.Kind() {
	case parser.KindCompleteShow:
		for _, sn := range s.Seasons {
			if sn.SeasonNumber > 0 {
				eps = append(eps, sn.Episodes...)
			}
		}
		return eps, len(eps) > 0, true
	case parser.KindMultiSeason, parser.KindSeasonPack:
		seasons := p.Seasons
		if len(seasons) == 0 {
			seasons = []int{p.Season}
		}
		complete = true
		for _, want := range seasons {
			listed := false
			for _, sn := range s.Seasons {
				if sn.SeasonNumber == want && len(sn.Episodes) > 0 {
					eps = append(eps, sn.Episodes...)
					listed = true
				}
			}
			complete = complete && listed
		}
		return eps, complete, true
	case parser.KindEpisode:
		if len(p.Episodes) == 0 || (p.Season == 0 && !p.SeasonExplicit) {
			return nil, false, false
		}
		want := map[int]bool{}
		for _, e := range p.Episodes {
			want[e] = true
		}
		for _, sn := range s.Seasons {
			if sn.SeasonNumber != p.Season {
				continue
			}
			for _, e := range sn.Episodes {
				if want[e.EpisodeNumber] {
					eps = append(eps, e)
					delete(want, e.EpisodeNumber)
				}
			}
		}
		return eps, len(want) == 0, true
	}
	return nil, false, false
}

// earliestAir is the earliest air date among eps; ok is false when none has one.
func earliestAir(eps []Episode) (time.Time, bool) {
	var first time.Time
	for _, e := range eps {
		t, err := time.Parse("2006-01-02", e.AirDate)
		if err != nil {
			continue
		}
		if first.IsZero() || t.Before(first) {
			first = t
		}
	}
	return first, !first.IsZero()
}

// coverLabel names what the release covers, for a reason: "S02E03", "season 2".
func coverLabel(p parser.Release) string {
	switch p.Kind() {
	case parser.KindEpisode:
		if len(p.Episodes) > 0 {
			return fmt.Sprintf("S%02dE%02d", p.Season, p.Episodes[0])
		}
	case parser.KindSeasonPack:
		return fmt.Sprintf("season %d", p.Season)
	}
	return "episodes"
}

func labelOf(s Series) string { return DescribeCandidates([]Series{s}) }

func labelsOf(ss []Series) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, labelOf(s))
	}
	return out
}

// joinAnd joins names as prose: "A and B", "A, B and C".
func joinAnd(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
