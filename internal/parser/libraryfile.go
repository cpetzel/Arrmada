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

// reNxERunJoin is what joins the tokens of one multi-episode run ("1x8-1x9", "1x8 & 1x9").
var reNxERunJoin = regexp.MustCompile(`^\s*[-&+]\s*$`)

// reNxEBareTail is a run followed by a bare number joined straight on ("1x8-9", "1x8&9"): a
// multi-episode form whose other episodes can't be told, so the name is left unread.
var reNxEBareTail = regexp.MustCompile(`^(?:-|\s*[&+]\s*)\d`)

// nxeLookalikes are tokens that are far more often an aspect ratio or a dimension than an
// episode. One is only read when it is set off by dashes AND the season folder it sits in
// says the same season.
var nxeLookalikes = map[string]bool{"16x9": true, "21x9": true, "32x9": true, "4x3": true, "4x4": true}

// ParseLibraryFile is Parse for a video file already in the library: name is the file's
// base name; folderSeason is the season its parent folder names, when inSeasonFolder says
// the parent is a season folder ("S01", "Season 1", "Stagione 1", "Specials" → 0).
//
// It returns exactly what Parse returns, except for a name Parse found no season, episode
// or pack marker in that carries one single-digit-episode NxE token (or one multi-episode
// run of them, "1x8-1x9") outside any brackets and before any quality tag, which is read
// as that episode when either
//
//   - the folder it sits in is the season folder of the same season ("S01/… 1x8 …"), or
//   - it sits in no season folder and is set off by dashes: "Title - 1x8 - Episode",
//     "Title - 1x8- Episode", "Title-1x8-Episode", or "Title - 1x8" ending the name.
//
// Never otherwise: a season folder saying another season, season 0 outside a specials or
// "Season 0" folder, an aspect-ratio lookalike (16x9, 21x9, 32x9, 4x3, 4x4) unless it is
// both set off by dashes and in its season's folder, a number glued on ("5.1x2", "1x2.5", "1x8-9"), or a second candidate
// token. The name is then read as if the episode were padded ("1x08"), so the title,
// tags and traits come out exactly as Parse reads that long-supported form.
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
		lookalike  bool
	}
	var runs []run
	for _, m := range locs {
		season, _ := strconv.Atoi(scan[m[2]:m[3]])
		ep, _ := strconv.Atoi(scan[m[4]:m[5]])
		if n := len(runs); n > 0 && runs[n-1].season == season && reNxERunJoin.MatchString(scan[runs[n-1].end:m[0]]) {
			last := &runs[n-1]
			last.end = m[1]
			last.eps = append(last.eps, ep)
			last.digits = append(last.digits, m[4])
			last.lookalike = false
			continue
		}
		runs = append(runs, run{start: m[0], end: m[1], season: season, eps: []int{ep}, digits: []int{m[4]},
			lookalike: nxeLookalikes[strings.ToLower(scan[m[0]:m[1]])]})
	}

	var pick *run
	for i := range runs {
		rn := &runs[i]
		if !nxeRunReads(scan, rn.start, rn.end, rn.season, rn.lookalike, folderSeason, inSeasonFolder) {
			if rn.lookalike {
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
	return got
}

// nxeRunReads reports whether the NxE run at scan[start:end] is the file's episode.
func nxeRunReads(scan string, start, end, season int, lookalike bool, folderSeason int, inSeasonFolder bool) bool {
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
	// Set off by dashes: one before it with a title before that, and one after it or
	// nothing at all ("Title - 1x8 - Episode", "Title - 1x8- Episode", "Title - 1x8").
	dashed := false
	if before := strings.TrimRight(scan[:start], " ."); strings.HasSuffix(before, "-") &&
		strings.TrimRight(strings.TrimSpace(before[:len(before)-1]), " .-") != "" {
		after := strings.TrimLeft(scan[end:], " .")
		dashed = after == "" || strings.HasPrefix(after, "-")
	}
	if lookalike {
		// "16x9", "4x4": only "Show - 4x4 - Title" in the Season 4 folder.
		return folderConfirms && dashed
	}
	return folderConfirms || dashed
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
