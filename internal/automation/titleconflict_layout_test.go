package automation

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Where a finished series download's files land when the episode titles in the filenames
// and the metadata's episode list disagree. Every case runs the real import sweep twice
// (the second pass is the re-process that fills a season's gaps) against a store-backed
// show and reports, for each episode of the season, which file it ended up with: "5:4"
// means episode 5 holds the file numbered E04 in its name, "7:-" means episode 7 is still
// empty. "held" means the download is in Needs you; "blocklisted" that the release was
// blocklisted for the show.
//
// was is the outcome on v0.3.3 and on main before the fix (both run this file unchanged);
// want is now. A change may only turn a wrong placement into "held" or into the right
// placement — never a right one into a wrong one.

// The owner's Nobody Wants This season 3: all ten episodes released at once, while the
// metadata still carried its pre-release listing. E01–E03 agree in both listings; the
// titles of E06 and E09 weren't in the stale one. Only the titles the owner's logs show
// are real — the rest are stand-ins that match nothing.
var nwtStale = map[int]string{
	1: "The Opener", 2: "The Second One", 3: "The Third One",
	4: "The Wedding Planners", 5: "Brian's Wedding", 6: "Episode 6",
	7: "May I Have This Dance", 8: "Watch What Happens", 9: "Episode 9", 10: "I Jew",
}

var nwtFixed = map[int]string{
	1: "The Opener", 2: "The Second One", 3: "The Third One",
	4: "Brian's Wedding", 5: "The Love Card", 6: "Heatwave",
	7: "Watch What Happens", 8: "One in a Million", 9: "Last Christmas", 10: "The Plunge",
}

const nwtPack = "Nobody.Wants.This.S03.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX"

var nwtFiles = []string{
	"Nobody.Wants.This.S03E01.The.Opener.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E02.The.Second.One.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E03.The.Third.One.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E04.Brians.Wedding.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E05.The.Love.Card.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E06.Heatwave.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E07.Watch.What.Happens.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E08.One.in.a.Million.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E09.Last.Christmas.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
	"Nobody.Wants.This.S03E10.The.Plunge.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv",
}

// nwtUntitled is the same pack named the way most packs are: numbers, no titles.
func nwtUntitled() []string {
	out := make([]string, 10)
	for i := range out {
		out[i] = fmt.Sprintf("Nobody.Wants.This.S03E%02d.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX.mkv", i+1)
	}
	return out
}

type layoutShow struct {
	title  string
	year   int
	season int
	titles map[int]string
}

var nwtShow = func(titles map[int]string) layoutShow {
	return layoutShow{title: "Nobody Wants This", year: 2024, season: 3, titles: titles}
}

// Parks and Recreation season 6 as TMDB lists it: the two-part "London" is one episode, so
// every release (numbered as TVDB does, London split in two) is one ahead from 6x03 on.
// This is what the title placement was built for.
var parksShow = layoutShow{title: "Parks and Recreation", year: 2009, season: 6, titles: map[int]string{
	1: "London", 2: "The Pawnee-Eagleton Tip-Off Classic", 3: "Doppelgängers", 4: "Gin It Up!", 5: "Filibuster",
}}

// A season the metadata numbers from one but the release one later throughout.
var shiftedShow = layoutShow{title: "Shifted Show", year: 2020, season: 1, titles: map[int]string{
	1: "Alpha Begins Here", 2: "Bravo Comes Next", 3: "Charlie Finishes It",
}}

type titleLayoutCase struct {
	name      string
	show      layoutShow
	download  string   // the torrent's name; "" for a single-file torrent (its one file)
	files     []string // the download's videos
	manual    bool     // grabbed by hand for the season: the quality gate is skipped
	was, want string
}

var titleLayoutCases = []titleLayoutCase{
	{
		name: "Nobody Wants This S03 pack, stale metadata (the owner's case)",
		show: nwtShow(nwtStale), download: nwtPack, files: nwtFiles,
		was:  "1:1 2:2 3:3 4:- 5:4 6:6 7:- 8:7 9:9 10:10 blocklisted",
		want: "held 1:- 2:- 3:- 4:- 5:- 6:- 7:- 8:- 9:- 10:-",
	},
	{
		// Grabbed by hand the gate is skipped, so the file numbered E05 overwrites the one
		// moved onto E05 (which goes to the recycle bin) — and E04 and E07 never arrive.
		name: "Nobody Wants This S03 pack, stale metadata, grabbed by hand",
		show: nwtShow(nwtStale), download: nwtPack, files: nwtFiles, manual: true,
		was:  "1:1 2:2 3:3 4:- 5:5 6:6 7:- 8:8 9:9 10:10",
		want: "held 1:- 2:- 3:- 4:- 5:- 6:- 7:- 8:- 9:- 10:-",
	},
	{
		name: "Nobody Wants This S03 pack, corrected metadata",
		show: nwtShow(nwtFixed), download: nwtPack, files: nwtFiles,
		was:  "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
		want: "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
	},
	{
		name: "Nobody Wants This S03 pack without titles, stale metadata",
		show: nwtShow(nwtStale), download: nwtPack, files: nwtUntitled(),
		was:  "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
		want: "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
	},
	{
		// The CAKES single: a number and no title imports by its number.
		name: "single episode, number only, stale metadata",
		show: nwtShow(nwtStale), files: []string{"Nobody.Wants.This.S03E07.1080p.WEB.h264-CAKES.mkv"},
		was:  "1:- 2:- 3:- 4:- 5:- 6:- 7:7 8:- 9:- 10:-",
		want: "1:- 2:- 3:- 4:- 5:- 6:- 7:7 8:- 9:- 10:-",
	},
	{
		// One file alone can't show the metadata is stale: its title names episode 5 and
		// nothing in the download disagrees. It is placed by title as before — the same
		// rule that files a lone Parks 6x03 correctly.
		name: "single episode with a title, stale metadata",
		show: nwtShow(nwtStale), files: []string{nwtFiles[3]},
		was:  "1:- 2:- 3:- 4:- 5:4 6:- 7:- 8:- 9:- 10:-",
		want: "1:- 2:- 3:- 4:- 5:4 6:- 7:- 8:- 9:- 10:-",
	},
	{
		// Likewise a partial pack whose moved file lands on an episode no other file in it
		// claims: nothing in the download contradicts the title.
		name: "partial pack, one file moved by title onto an unclaimed episode",
		show: nwtShow(nwtStale), download: "Nobody.Wants.This.S03.Part.1.1080p.NF.WEB-DL.DDP5.1.Atmos.H.264-FLUX", files: nwtFiles[:4],
		was:  "1:1 2:2 3:3 4:- 5:4 6:- 7:- 8:- 9:- 10:- blocklisted",
		want: "1:1 2:2 3:3 4:- 5:4 6:- 7:- 8:- 9:- 10:- blocklisted",
	},
	{
		// The case title placement exists for: the two-part file keeps its two numbers
		// and every later file moves back one, onto an episode nothing else claims except
		// the two-parter's second half.
		name: "Parks and Recreation S06, TMDB merged the two-part London",
		show: parksShow, download: "Parks.and.Recreation.S06.1080p.WEB-DL.x264-GRP", files: []string{
			"Parks and Recreation - 6x01 & 6x02 - London.mkv",
			"Parks and Recreation - 6x03 - The Pawnee-Eagleton Tip-Off Classic.mkv",
			"Parks and Recreation - 6x04 - Doppelgängers.mkv",
			"Parks and Recreation - 6x05 - Gin It Up!.mkv",
			"Parks and Recreation - 6x06 - Filibuster.mkv",
		},
		was:  "1:1+2 2:1+2 3:4 4:5 5:6",
		want: "1:1+2 2:1+2 3:4 4:5 5:6",
	},
	{
		name: "Parks and Recreation S06, grabbed by hand",
		show: parksShow, download: "Parks.and.Recreation.S06.1080p.WEB-DL.x264-GRP", manual: true, files: []string{
			"Parks and Recreation - 6x01 & 6x02 - London.mkv",
			"Parks and Recreation - 6x03 - The Pawnee-Eagleton Tip-Off Classic.mkv",
			"Parks and Recreation - 6x04 - Doppelgängers.mkv",
			"Parks and Recreation - 6x05 - Gin It Up!.mkv",
			"Parks and Recreation - 6x06 - Filibuster.mkv",
		},
		was:  "1:1+2 2:3 3:4 4:5 5:6",
		want: "1:1+2 2:3 3:4 4:5 5:6",
	},
	{
		name: "Nobody Wants This S03 pack, corrected metadata, grabbed by hand",
		show: nwtShow(nwtFixed), download: nwtPack, files: nwtFiles, manual: true,
		was:  "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
		want: "1:1 2:2 3:3 4:4 5:5 6:6 7:7 8:8 9:9 10:10",
	},
	{
		name: "whole pack numbered one ahead of the metadata, every file titled",
		show: shiftedShow, download: "Shifted.Show.S01.1080p.WEB-DL.x264-GRP", files: []string{
			"Shifted.Show.S01E02.Alpha.Begins.Here.1080p.WEB-DL.x264-GRP.mkv",
			"Shifted.Show.S01E03.Bravo.Comes.Next.1080p.WEB-DL.x264-GRP.mkv",
			"Shifted.Show.S01E04.Charlie.Finishes.It.1080p.WEB-DL.x264-GRP.mkv",
		},
		was:  "1:2 2:3 3:4",
		want: "1:2 2:3 3:4",
	},
	{
		// Two copies of one episode, both by number: not a title disagreement. The first
		// is placed and the second is weighed against it, as before.
		name: "two copies of one episode by number",
		show: shiftedShow, download: "Shifted.Show.S01.1080p.WEB-DL.x264-GRP", files: []string{
			"Shifted.Show.S01E01.1080p.WEB-DL.x264-GRP.mkv",
			"Shifted.Show.S01E01.REPACK.1080p.WEB-DL.x264-GRP.mkv",
			"Shifted.Show.S01E02.1080p.WEB-DL.x264-GRP.mkv",
		},
		was:  "1:1 2:2 3:- blocklisted",
		want: "1:1 2:2 3:- blocklisted",
	},
}

func TestTitlePlacementLayoutMatrix(t *testing.T) {
	var changed []string
	for _, tc := range titleLayoutCases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTitleLayoutCase(t, tc)
			if got != tc.want {
				t.Errorf("got  %s\nwant %s\n(was %s)", got, tc.want, tc.was)
			}
			if tc.was != tc.want {
				changed = append(changed, fmt.Sprintf("%s: %s → %s", tc.name, tc.was, tc.want))
			}
		})
	}
	for _, c := range changed {
		t.Log("changed: " + c)
	}
}

// runTitleLayoutCase imports one case's download through the sweep and describes where
// its files landed.
func runTitleLayoutCase(t *testing.T, tc titleLayoutCase) string {
	t.Helper()
	h := newStallHarness(t)
	h.c.imp = library.NewImporter(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	id := seedTitledSeason(t, h, tc.show)
	hash, name, _ := dropDownload(t, h, tc.download, tc.files)
	if tc.manual {
		h.c.recordSeriesGrab(h.ctx, id, name, "Fake", "", hash)
		h.c.markGrab(h.ctx, hash, true, GrabScope{Season: tc.show.season})
	}
	h.c.ImportSeriesDownloads(h.ctx)
	h.c.ImportSeriesDownloads(h.ctx)
	return describeSeason(t, h, id, tc.show.season, hash, tc.files)
}

// seedTitledSeason adds a show with one aired, monitored season carrying titles.
func seedTitledSeason(t *testing.T, h *stallHarness, sh layoutShow) int64 {
	t.Helper()
	repo := series.NewRepo(h.c.db)
	sr, err := repo.Create(h.ctx, series.Series{TMDBID: 77, Title: sh.title, Year: sh.year, Monitored: true, Status: "Ended"})
	if err != nil {
		t.Fatal(err)
	}
	season := series.Season{SeasonNumber: sh.season, Monitored: true}
	for n := 1; n <= len(sh.titles); n++ {
		season.Episodes = append(season.Episodes, series.Episode{
			SeasonNumber: sh.season, EpisodeNumber: n, Title: sh.titles[n],
			AirDate: "2025-10-23", Runtime: 30, Monitored: true,
		})
	}
	if err := repo.InsertSeasons(h.ctx, sr.ID, []series.Season{season}); err != nil {
		t.Fatal(err)
	}
	return sr.ID
}

// dropDownload writes a finished torrent's videos (each a distinct size, so the library
// can say which one it holds) and lists it in the TV category of the fake client.
// download "" is a single-file torrent of files[0].
func dropDownload(t *testing.T, h *stallHarness, download string, files []string) (hash, name, content string) {
	t.Helper()
	dir := t.TempDir()
	if download != "" {
		dir = filepath.Join(dir, download)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range files {
		writeSparse(t, filepath.Join(dir, f), layoutFileSize(i))
	}
	name, content = download, dir
	if download == "" {
		name = strings.TrimSuffix(files[0], filepath.Ext(files[0]))
		content = filepath.Join(dir, files[0])
	}
	hash = hashFor(name)
	h.qbit.mu.Lock()
	h.qbit.torrents = append(h.qbit.torrents, map[string]any{
		"hash": hash, "name": name, "state": "uploading", "progress": 1.0,
		"size": int64(len(files)) * layoutFileSize(0), "amount_left": 0, "category": seriesCategory, "content_path": content,
	})
	h.qbit.mu.Unlock()
	return hash, name, content
}

func layoutFileSize(i int) int64 { return int64(60+i) << 20 }

func writeSparse(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// describeSeason renders "held"/"blocklisted" and, per episode, the number(s) in the name
// of the file it holds — the file found by its size.
func describeSeason(t *testing.T, h *stallHarness, id int64, season int, hash string, files []string) string {
	t.Helper()
	var parts []string
	if reviews, _ := h.c.ListReviews(h.ctx); len(reviews) > 0 {
		for _, r := range reviews {
			if r.Hash == hash {
				parts = append(parts, "held")
				break
			}
		}
	}
	s, err := h.c.series.Get(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range s.Seasons {
		if sn.SeasonNumber != season {
			continue
		}
		for _, ep := range sn.Episodes {
			holds := "-"
			if ep.HasFile {
				holds = "?"
				for i, f := range files {
					if ep.SizeBytes == layoutFileSize(i) {
						holds = fileNumbers(f)
					}
				}
			}
			parts = append(parts, fmt.Sprintf("%d:%s", ep.EpisodeNumber, holds))
		}
	}
	if h.blocklistCount(t) > 0 {
		parts = append(parts, "blocklisted")
	}
	return strings.Join(parts, " ")
}

// fileNumbers is the episode number(s) a filename states: "4", or "1+2" for a two-parter.
func fileNumbers(name string) string {
	eps := parser.Parse(name).Episodes
	out := make([]string, len(eps))
	for i, e := range eps {
		out[i] = strconv.Itoa(e)
	}
	return strings.Join(out, "+")
}
