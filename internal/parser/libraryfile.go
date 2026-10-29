package parser

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Single-digit-episode NxE ("1x8") in the name of a file already in the library (#81).
//
// Parse needs a 2+ digit episode in the NxE form ("1x08") so an aspect ratio ("16x9") or a
// dimension ("4x4") in a release name is never read as an episode. Some libraries are named
// that way anyway — "11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1].mkv" in an
// "S01" folder — and every file of such a show parsed with no episode, so a rescan found
// none of them. ParseLibraryFile reads that form for library files only, and only where it
// can't be anything else.

// reNxESingle is a single-digit-episode NxE token. The \b on both sides keeps "1x8x9",
// "S01x8", "1080x8" and "1x80" out of it.
var reNxESingle = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d)\b`)

// reNxEListJoin joins two tokens of one multi-episode list: "1x8 & 1x9", "1x8+1x9". The
// only other join is a bare "-" with no space ("1x8-1x9"): a SPACED dash separates two
// tokens ("Kids 2x2 - 2x5 - Title"), it doesn't join them.
var reNxEListJoin = regexp.MustCompile(`^\s*[&+]\s*$`)

// nxeMaxStep is how far apart two joined episodes may be: "1x7-1x9" is a range, while
// "1x1-1x9" is far more likely two numbers that aren't one.
const nxeMaxStep = 3

// reNxEBareTail is a run followed by a bare number joined straight on ("1x8-9", "1x8&9"): a
// multi-episode form whose other episodes can't be told, so the name is left unread.
var reNxEBareTail = regexp.MustCompile(`^(?:[-–—]|\s*[&+]\s*)\d`)

// nxeLookalikes are tokens that are far more often an aspect ratio or a dimension than an
// episode ("16x9", "4x4", lumber's "2x4"). One is only read when it is set off by dashes AND
// the season folder it sits in says the same season; one that isn't read doesn't make a name
// ambiguous ("Show - 1x8 - 16x9 Edition" is 1x8). A token whose season and episode are the
// same ("3x3", "2x2", "1x1") is held to the same bar — "Speedcubing - 3x3", the show "3x3
// Eyes" — but one that isn't read is still a second candidate.
var nxeLookalikes = map[string]bool{"16x9": true, "21x9": true, "32x9": true, "4x3": true, "4x4": true, "2x4": true, "2x6": true}

// ParseLibraryFile is Parse for a video file already in the library: name is the file's
// base name; folderSeason is the season its parent folder names, when inSeasonFolder says
// the parent is a season folder ("S01", "Season 1", "Stagione 1", "Specials" → 0).
//
// It returns exactly what Parse returns, except for a name Parse found no season, episode
// or pack marker in that carries one single-digit-episode NxE token (or one multi-episode
// run of them, "1x8-1x9", "1x8 & 1x9", ascending and close together) outside any brackets
// and before any quality tag, which is read as that episode when either
//
//   - the folder it sits in is the season folder of the same season ("S01/… 1x8 …"), or
//   - it is set off by dashes after a title: "Title - 1x8 - Episode", "Title - 1x8- Episode",
//     "Title-1x8-Episode", "Title – 1x8", or "Title - 1x8" ending the name.
//
// Never otherwise: a season folder saying another season, season 0 outside a specials or
// "Season 0" folder, a lookalike (16x9, 21x9, 32x9, 4x3, 4x4, 2x4, 2x6, or any NxN with the
// same number twice) unless it is both set off by dashes and in its season's folder, a
// number glued on ("5.1x2", "1x2.5", "1x8-9"), a second candidate token, or a token with
// no title before it ("3x3 Eyes 08", "1x8 - Pilot") unless the name is nothing but the
// token ("1x8.mkv"). The name is then read as if the episode were padded ("1x08"), so the
// title, tags and traits come out as Parse reads that long-supported form.
func ParseLibraryFile(name string, folderSeason int, inSeasonFolder bool) Release {
	r := Parse(name)
	if r.IsTV() {
		return r
	}
	base := strings.TrimSpace(name)
	for _, ext := range videoExts {
		if strings.EqualFold(filepath.Ext(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	// The same view Parse matches against: underscores as spaces, brackets blanked. Both
	// keep the length, so indices line up with base.
	scan := maskBracketed(strings.ReplaceAll(base, "_", " "))
	locs := reNxESingle.FindAllStringSubmatchIndex(scan, -1)
	if len(locs) == 0 {
		return r
	}

	type run struct {
		start, end int
		season     int
		eps        []int
		digits     []int // index of each token's episode digit, for the padding
		aspect     bool  // one token, an aspect ratio or dimension
		twin       bool  // one token, the same number twice ("3x3")
	}
	var runs []run
	for _, m := range locs {
		season, _ := strconv.Atoi(scan[m[2]:m[3]])
		ep, _ := strconv.Atoi(scan[m[4]:m[5]])
		if n := len(runs); n > 0 {
			last := &runs[n-1]
			gap := scan[last.end:m[0]]
			prev := last.eps[len(last.eps)-1]
			if last.season == season && ep > prev && ep-prev <= nxeMaxStep && (gap == "-" || reNxEListJoin.MatchString(gap)) {
				last.end = m[1]
				last.eps = append(last.eps, ep)
				last.digits = append(last.digits, m[4])
				last.aspect, last.twin = false, false
				continue
			}
		}
		runs = append(runs, run{start: m[0], end: m[1], season: season, eps: []int{ep}, digits: []int{m[4]},
			aspect: nxeLookalikes[strings.ToLower(scan[m[0]:m[1]])], twin: season == ep})
	}

	var pick *run
	for i := range runs {
		rn := &runs[i]
		if !nxeRunReads(base, scan, rn.start, rn.end, rn.season, rn.aspect || rn.twin, folderSeason, inSeasonFolder) {
			if rn.aspect {
				continue // an aspect ratio beside the episode doesn't make the name ambiguous
			}
			return r // a second, unreadable candidate: no telling which is the episode
		}
		if pick != nil {
			return r // two readable candidates
		}
		pick = rn
	}
	if pick == nil {
		return r
	}

	// Pad each episode digit ("1x8" → "1x08") and read the name as Parse reads that form.
	var b strings.Builder
	prev := 0
	for _, d := range pick.digits {
		b.WriteString(base[prev:d])
		b.WriteByte('0')
		prev = d
	}
	b.WriteString(base[prev:])
	got := Parse(b.String())
	if got.Season != pick.season || !got.SeasonExplicit || !nxeCovers(got.Episodes, pick.eps) {
		return r // read differently than the token says: leave the name unread
	}
	got.Title = strings.TrimSpace(strings.TrimRight(got.Title, " -–—"))
	if got.Title == "" && !nxeWholeStem(base, pick.start, pick.end) {
		// No title: nothing would say whose episode it is, and a name without one is taken
		// on trust in the show's own folder. Only a bare "1x8.mkv" may be read so.
		return r
	}
	return got
}

// nxeRunReads reports whether the NxE run at scan[start:end] is the file's episode. strict
// is a lookalike or an NxN twin, which needs both the dashes and the season folder.
func nxeRunReads(base, scan string, start, end, season int, strict bool, folderSeason int, inSeasonFolder bool) bool {
	if inSeasonFolder && folderSeason != season {
		return false // the folder says another season
	}
	folderConfirms := inSeasonFolder && folderSeason == season
	if season == 0 && !folderConfirms {
		return false // a special only inside "Specials" / "Season 0"
	}
	// A number glued on: "5.1x2" (audio channels), "1x2.5", "1x8-9".
	if start >= 2 && strings.ContainsRune(".,", rune(scan[start-1])) && isDigit(scan[start-2]) {
		return false
	}
	if end+1 < len(scan) && strings.ContainsRune(".,", rune(scan[end])) && isDigit(scan[end+1]) {
		return false
	}
	if reNxEBareTail.MatchString(scan[end:]) {
		return false
	}
	// The episode marker comes before the release tags: one after a resolution, source,
	// codec or audio tag is part of them ("1080p.16x9", "DTS.5x1").
	if reQualityStart.MatchString(scan[:start]) {
		return false
	}
	// Nothing before it: the token opens the name, where it is far more often the show's
	// own title ("3x3 Eyes 08", "2x2 Club Ep 5"). Only a name that is nothing but the token.
	if strings.Trim(scan[:start], " .-–—") == "" {
		return nxeWholeStem(base, start, end) && folderConfirms && !strict
	}
	// Set off by dashes: one before it with a title before that, and one after it or
	// nothing at all ("Title - 1x8 - Episode", "Title - 1x8- Episode", "Title - 1x8").
	dashed := false
	if before, ok := cutDashSuffix(strings.TrimRight(scan[:start], " .")); ok && strings.Trim(before, " .-–—") != "" {
		after := strings.TrimLeft(scan[end:], " .")
		dashed = after == "" || hasDashPrefix(after)
	}
	if strict {
		// "16x9", "4x4", "3x3": only "Show - 4x4 - Title" in the Season 4 folder.
		return folderConfirms && dashed
	}
	return folderConfirms || dashed
}

// nxeWholeStem reports whether base is nothing but the run at [start:end] ("1x8").
func nxeWholeStem(base string, start, end int) bool {
	return strings.TrimSpace(base[:start]) == "" && strings.TrimSpace(base[end:]) == ""
}

var nxeDashes = []string{"-", "–", "—"}

func cutDashSuffix(s string) (string, bool) {
	for _, d := range nxeDashes {
		if strings.HasSuffix(s, d) {
			return strings.TrimSuffix(s, d), true
		}
	}
	return s, false
}

func hasDashPrefix(s string) bool {
	for _, d := range nxeDashes {
		if strings.HasPrefix(s, d) {
			return true
		}
	}
	return false
}

// nxeCovers reports whether Parse's reading of the padded name starts at the run's first
// episode and holds every episode the run names (a hyphenated pair is a range, so "1x7-1x9"
// also holds 8).
func nxeCovers(got, eps []int) bool {
	if len(got) == 0 || got[0] != eps[0] {
		return false
	}
	have := map[int]bool{}
	for _, n := range got {
		have[n] = true
	}
	for _, n := range eps {
		if !have[n] {
			return false
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
