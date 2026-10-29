package parser

import (
	"fmt"
	"reflect"
	"testing"
)

// #81: a library named "Title - 1x8- Episode" (single-digit episode, NxE form) in "S01"
// folders read no episode at all, so a rescan found none of the files. ParseLibraryFile
// reads that form for files on disk, only where it can't be anything else.
func TestParseLibraryFileSingleDigitNxE(t *testing.T) {
	const reporter = "11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv"
	cases := []struct {
		name         string
		folderSeason int
		inSeason     bool
		season       int
		episodes     []int // nil: read exactly as Parse reads it
	}{
		// The reporter's files: dashes, and their S01 folder, each enough on its own.
		{reporter, 1, true, 1, []int{8}},
		{reporter, 0, false, 1, []int{8}},
		{"11.22.63 - 1x1- The Rabbit Hole [HDTVMux1080p Ita 5.1] NovaRip.mkv", 1, true, 1, []int{1}},
		{"Show - 1x8 - Title.mkv", 0, false, 1, []int{8}},
		{"Show-1x8-Title.mkv", 0, false, 1, []int{8}},
		{"Show - 2x3.mkv", 0, false, 2, []int{3}},
		{"Show_-_1x8_-_Title.mkv", 0, false, 1, []int{8}},
		{"Show - 12x3 - Title.mkv", 0, false, 12, []int{3}},
		{"Show - 1x8 - Title [1080p].mkv", 0, false, 1, []int{8}},
		// No dashes: only the season folder can say so.
		{"Show 1x8 Title.mkv", 1, true, 1, []int{8}},
		{"Show.1x8.Title.mkv", 1, true, 1, []int{8}},
		{"Show 1x8 Title.mkv", 0, false, 0, nil},
		{"Show.1x8.Title.mkv", 0, false, 0, nil},
		// Multi-episode runs, and a hyphenated pair as a range like the padded form.
		{"Show - 1x8-1x9 - Title.mkv", 0, false, 1, []int{8, 9}},
		{"Show - 1x8 & 1x9 - Title.mkv", 1, true, 1, []int{8, 9}},
		{"Show - 1x7-1x9 - Title.mkv", 0, false, 1, []int{7, 8, 9}},
		// Multi-episode forms whose other episodes can't be told: left unread.
		{"Show - 1x8x9 - Title.mkv", 1, true, 0, nil},
		{"Show - 1x8-9 - Title.mkv", 1, true, 0, nil},
		{"Show - 1x8&9 - Title.mkv", 1, true, 0, nil},
		// A season folder that says another season wins over the dashes.
		{"Show - 2x3 - Title.mkv", 1, true, 0, nil},
		// Season 0 only inside a specials / Season 0 folder.
		{"Show - 0x1 - Special.mkv", 0, false, 0, nil},
		{"Show - 0x1 - Special.mkv", 1, true, 0, nil},
		{"Show - 0x1 - Special.mkv", 0, true, 0, []int{1}},
		// Aspect ratios and dimensions: never on dashes alone.
		{"Movie - 16x9.mkv", 0, false, 0, nil},
		{"Movie - 16x9 - Widescreen.mkv", 0, false, 0, nil},
		{"Car Review - 4x4 - Offroad.mkv", 0, false, 0, nil},
		{"Car Review - 4x3 - Offroad.mkv", 0, false, 0, nil},
		{"Some.Movie.2019.1080p.16x9.BluRay.x264-GRP.mkv", 0, false, 0, nil},
		{"Some.Movie.2019.4x4.Offroad.1080p.BluRay-GRP.mkv", 0, false, 0, nil},
		// …but set off by dashes in the season folder that says the same season, one is.
		{"Show - 4x4 - Title.mkv", 4, true, 4, []int{4}},
		{"Show - 16x9 - Title.mkv", 16, true, 16, []int{9}},
		{"Show 4x4 Offroad.mkv", 4, true, 0, nil},
		{"Show.1080p.16x9.mkv", 16, true, 0, nil},
		// An aspect ratio beside the real episode doesn't hide it.
		{"Show - 1x8 - 16x9 Edition.mkv", 0, false, 1, []int{8}},
		// After the release tags it is one of them, folder or not.
		{"Some.Movie.2019.1080p.BluRay.DTS.5x1.x264-GRP.mkv", 5, true, 0, nil},
		{"Show.1080p.WEB-DL - 1x8 - Title.mkv", 1, true, 0, nil},
		{"Show 720p 1x2.mkv", 1, true, 0, nil},
		// Inside brackets: a tag, never the episode.
		{"Show [1x2] Title.mkv", 1, true, 0, nil},
		{"Show - [1x2] - Title.mkv", 0, false, 0, nil},
		{"Show (1x8) Title.mkv", 1, true, 0, nil},
		// Glued to other numbers: "5.1x2" channels, "1x2.5".
		{"Show 5.1x2.mkv", 1, true, 0, nil},
		{"Show - 1x2.5 - Title.mkv", 1, true, 0, nil},
		// Two candidates: no telling which.
		{"Show - 1x8 - Part 2x3.mkv", 0, false, 0, nil},
		{"Show - 1x8 - Part 1x9.mkv", 1, true, 0, nil},
		// A dash with no title before it: nothing sets the token off.
		{"- 1x8 - Title.mkv", 0, false, 0, nil},
		{"1x8 - Title.mkv", 0, false, 0, nil},
		// Nothing before the token, even in its season's folder: far more often the show's
		// own title ("3x3 Eyes", "2x2 Club"). Only a name that is the token alone.
		{"1x8 - Title.mkv", 1, true, 0, nil},
		{"- 1x8 - Title.mkv", 1, true, 0, nil},
		{"3x3 Eyes 08.mkv", 3, true, 0, nil},
		{"3x3 Eyes 05.mkv", 3, true, 0, nil},
		{"2x2 Club Ep 5.mkv", 2, true, 0, nil},
		{"[Grp] 1x8 - Title.mkv", 1, true, 0, nil},
		{"1x8.mkv", 1, true, 1, []int{8}},
		{"1x8.mkv", 0, false, 0, nil},
		// A spaced dash separates two tokens, it doesn't join them into a range.
		{"Kids 2x2 - 2x5 - Title.mkv", 2, true, 0, nil},
		{"Kids 2x2 - 2x5 - Title.mkv", 0, false, 0, nil},
		{"Show - 1x1 - 1x8 - Ep.mkv", 1, true, 0, nil},
		{"Show - 1x1 - 1x8 - Ep.mkv", 0, false, 0, nil},
		{"Show - 1x3 - 1x4 - Ep.mkv", 1, true, 0, nil},
		// A joined run goes up, a step at a time.
		{"Show - 1x9-1x8 - Ep.mkv", 1, true, 0, nil},
		{"Show - 1x1-1x9 - Ep.mkv", 1, true, 0, nil},
		{"Show - 1x8 & 1x8 - Ep.mkv", 1, true, 0, nil},
		{"Show - 1x8 + 1x9 - Ep.mkv", 0, false, 1, []int{8, 9}},
		// The same number twice, and dimensions: only dashed AND in that season's folder.
		{"Speedcubing - 3x3.mkv", 0, false, 0, nil},
		{"Rubik - 2x2 - Tutorial.mkv", 0, false, 0, nil},
		{"Lego - 2x4 - Brick.mkv", 0, false, 0, nil},
		{"Show 3x3 Title.mkv", 3, true, 0, nil},
		{"Show - 3x3 - Title.mkv", 3, true, 3, []int{3}},
		{"Show - 2x4 - Title.mkv", 2, true, 2, []int{4}},
		{"11.22.63 - 1x1- The Rabbit Hole.mkv", 0, false, 0, nil},
		// An unread NxN twin is a second candidate, not a lookalike to step over.
		{"Show - 2x2 - 2x5.mkv", 2, true, 0, nil},
		// En and em dashes are dashes.
		{"Show – 1x8 – Title.mkv", 0, false, 1, []int{8}},
		{"Show — 1x8.mkv", 0, false, 1, []int{8}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s|%d|%v", c.name, c.folderSeason, c.inSeason), func(t *testing.T) {
			got := ParseLibraryFile(c.name, c.folderSeason, c.inSeason)
			if c.episodes == nil {
				if want := Parse(c.name); !reflect.DeepEqual(got, want) {
					t.Errorf("read as S%d %v, want Parse's reading (S%d %v)", got.Season, got.Episodes, want.Season, want.Episodes)
				}
				return
			}
			if got.Season != c.season || !reflect.DeepEqual(got.Episodes, c.episodes) || !got.SeasonExplicit {
				t.Errorf("read as S%d %v (explicit %v), want S%d %v", got.Season, got.Episodes, got.SeasonExplicit, c.season, c.episodes)
			}
		})
	}
}

// A name the rule reads comes out exactly as the padded "1x08" form reads — title, tags,
// traits and all — so nothing but the episode is new.
func TestParseLibraryFileReadsAsThePaddedForm(t *testing.T) {
	for _, c := range []struct{ name, padded string }{
		{"11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv", "11.22.63 - 1x08- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv"},
		{"Show - 1x8-1x9 - Title.mkv", "Show - 1x08-1x09 - Title.mkv"},
		{"Show_-_1x8_-_Title.mkv", "Show_-_1x08_-_Title.mkv"},
		{"[Group] Show - 1x8 - Title [1080p].mkv", "[Group] Show - 1x08 - Title [1080p].mkv"},
	} {
		got, want := ParseLibraryFile(c.name, 0, false), Parse(c.padded)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n got  %#v\n want %#v", c.name, got, want)
		}
	}
	if r := ParseLibraryFile("Show – 1x8 – Title.mkv", 0, false); r.Title != "Show" {
		t.Errorf("en-dash name: title %q, want %q (no trailing dash)", r.Title, "Show")
	}
	r := ParseLibraryFile("11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv", 1, true)
	if r.Title != "11 22 63" || r.Resolution != Res1080p {
		t.Errorf("title %q resolution %q, want %q 1080p", r.Title, r.Resolution, "11 22 63")
	}
}

// Anything Parse already reads a season, episode or pack from is returned untouched, even
// when a single-digit token sits in it too.
func TestParseLibraryFileLeavesParsedNamesAlone(t *testing.T) {
	for _, name := range []string{
		"Show.S01E08.1080p.WEB-DL.x264-GRP.mkv",
		"Show - S01E08 - 1x9 Title.mkv",
		"Show - 1x08 - Title.mkv",
		"Show - 1x10-1x9 - Title.mkv",
		"Show.S02.1080p.BluRay.x264-GRP",
		"Show Season 2 - 1x8 - Title.mkv",
		"[SubsPlease] Frieren - 05 (1080p) [ABCD1234].mkv",
		"Frieren S2 29.mkv",
		"The.Daily.Show.2024.10.03.1080p.WEB.h264-GRP.mkv",
	} {
		for _, in := range []bool{false, true} {
			if got, want := ParseLibraryFile(name, 1, in), Parse(name); !reflect.DeepEqual(got, want) {
				t.Errorf("%q (season folder %v): read as S%d %v, want Parse's S%d %v", name, in, got.Season, got.Episodes, want.Season, want.Episodes)
			}
		}
	}
}
