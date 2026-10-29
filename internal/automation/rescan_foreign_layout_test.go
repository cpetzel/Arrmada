package automation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Which file a rescan records for each episode, and what the duplicate check then offers
// for deletion, when a file that isn't the show's — another show's ("Beta - 1x02"), or a
// name with no title ("S01E02.mkv") — sits beside the show's own episodes. Every case runs
// the real Refresh & rescan and SeriesDuplicates against a store-backed show "Show (2010)"
// over sparse files in a temp dir, across the layouts a show is kept in.
//
// The outcome reads "E1=<file> E2=<file> E3=<file> | dup E2 keep=<file> del=<files>":
// the file each episode has after the rescan ("-" for none, "refused" for a rescan that
// changed nothing), then every episode the duplicate check lists, the copy it keeps and
// the ones it offers to delete.
//
// was is v0.3.3's outcome and main is 931de3b3's (both run this file unchanged; "" means
// the same as want); want is now. The only differences allowed: a file whose name isn't the
// show's never displaces the file an episode has recorded, and the show's named copy is
// never offered for deletion because of it. Another show's file an earlier rescan already
// recorded as the episode gives way to the show's own copy, and is never offered for
// deletion either ("foreign already recorded"). A file named as another show still fills
// an episode with no file in a folder provably the show's own ("foreign fills"), as before.

type foreignFile struct {
	name string
	mb   int64
	ep   int  // the episode it's recorded as (0: on disk, not recorded)
	gone bool // recorded, not on disk
}

type foreignScenario struct {
	name    string
	files   []foreignFile
	library bool // another library show, "Beta" (2012), is in the library
}

var foreignScenarios = []foreignScenario{
	{name: "foreign displaces", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Beta - 1x02 - Other.mkv", mb: 90},
	}},
	{name: "foreign smaller", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Beta - 1x02 - Other.mkv", mb: 30},
	}},
	{name: "foreign higher res", files: []foreignFile{
		{name: "Show - S01E02 - 720p.mkv", mb: 60, ep: 2},
		{name: "Beta - 1x02 - Other - 1080p.mkv", mb: 50},
	}},
	{name: "foreign fills", files: []foreignFile{
		{name: "Beta - 1x03 - Other.mkv", mb: 90},
	}},
	{name: "foreign beside a gone file", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2, gone: true},
		{name: "Beta - 1x02 - Other.mkv", mb: 90},
	}},
	{name: "untitled displaces", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "S01E02.mkv", mb: 90},
	}},
	{name: "untitled fills", files: []foreignFile{
		{name: "S01E03.mkv", mb: 90},
	}},
	{name: "library show's file", library: true, files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Beta - S01E02.mkv", mb: 90},
	}},
	{name: "foreign already recorded", files: []foreignFile{
		{name: "Beta - 1x02 - Other.mkv", mb: 90, ep: 2},
		{name: "Show - S01E02.mkv", mb: 60},
	}},
	{name: "foreign recorded, smaller", files: []foreignFile{
		{name: "Beta - 1x02 - Other.mkv", mb: 30, ep: 2},
		{name: "Show - S01E02.mkv", mb: 60},
	}},
	{name: "untitled recorded, own bigger", files: []foreignFile{
		{name: "S01E02.mkv", mb: 60, ep: 2},
		{name: "Show - S01E02.mkv", mb: 90},
	}},
	{name: "untitled recorded, own smaller", files: []foreignFile{
		{name: "S01E02.mkv", mb: 90, ep: 2},
		{name: "Show - S01E02.mkv", mb: 60},
	}},
	{name: "foreign two-episode file", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Beta - S01E02-E03 - Other.mkv", mb: 90},
	}},
	{name: "own better resolution", files: []foreignFile{
		{name: "Show - S01E02 - 720p.mkv", mb: 60, ep: 2},
		{name: "Show - S01E02 - 1080p.mkv", mb: 50},
	}},
	{name: "own bigger copy", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Show.S01E02.WEB-DL-GRP.mkv", mb: 90},
	}},
	{name: "own smaller copy", files: []foreignFile{
		{name: "Show - S01E02.mkv", mb: 60, ep: 2},
		{name: "Show.S01E02.WEB-DL-GRP.mkv", mb: 30},
	}},
	{name: "own fills", files: []foreignFile{
		{name: "Show - S01E03.mkv", mb: 60},
	}},
}

// The folders a show's season 1 is kept in, relative to the TV folder.
var foreignLayouts = []struct{ name, dir string }{
	{"standard", "Show (2010)/Season 1"},
	{"flat", "Show (2010)"},
	{"S01 folder", "Show/S01"},
	{"category", "Anime/Show/Season 1"},
	{"franchise S01, issue 56", "Star Trek/S01"},
	{"S01 in the TV folder, issue 56", "S01"},
}

// want, by "layout|scenario"; was and main only where they differ from want.
var foreignWant = map[string]string{
	"standard|foreign displaces":                                    "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|foreign smaller":                                      "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|foreign higher res":                                   "E1=Show - S01E01.mkv E2=Show - S01E02 - 720p.mkv E3=-",
	"standard|foreign fills":                                        "E1=Show - S01E01.mkv E2=- E3=Beta - 1x03 - Other.mkv",
	"standard|foreign beside a gone file":                           "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=-",
	"standard|untitled displaces":                                   "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|untitled fills":                                       "E1=Show - S01E01.mkv E2=- E3=S01E03.mkv",
	"standard|library show's file":                                  "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|foreign already recorded":                             "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|foreign recorded, smaller":                            "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|untitled recorded, own bigger":                        "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|untitled recorded, own smaller":                       "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"standard|foreign two-episode file":                             "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=Beta - S01E02-E03 - Other.mkv",
	"standard|own better resolution":                                "E1=Show - S01E01.mkv E2=Show - S01E02 - 1080p.mkv E3=- | dup E2 keep=Show - S01E02 - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"standard|own bigger copy":                                      "E1=Show - S01E01.mkv E2=Show.S01E02.WEB-DL-GRP.mkv E3=- | dup E2 keep=Show.S01E02.WEB-DL-GRP.mkv del=Show - S01E02.mkv",
	"standard|own smaller copy":                                     "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"standard|own fills":                                            "E1=Show - S01E01.mkv E2=- E3=Show - S01E03.mkv",
	"flat|foreign displaces":                                        "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|foreign smaller":                                          "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|foreign higher res":                                       "refused",
	"flat|foreign fills":                                            "E1=Show - S01E01.mkv E2=- E3=Beta - 1x03 - Other.mkv",
	"flat|foreign beside a gone file":                               "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=-",
	"flat|untitled displaces":                                       "refused",
	"flat|untitled fills":                                           "refused",
	"flat|library show's file":                                      "refused",
	"flat|foreign already recorded":                                 "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|foreign recorded, smaller":                                "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|untitled recorded, own bigger":                            "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|untitled recorded, own smaller":                           "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"flat|foreign two-episode file":                                 "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=Beta - S01E02-E03 - Other.mkv",
	"flat|own better resolution":                                    "E1=Show - S01E01.mkv E2=Show - S01E02 - 1080p.mkv E3=- | dup E2 keep=Show - S01E02 - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"flat|own bigger copy":                                          "E1=Show - S01E01.mkv E2=Show.S01E02.WEB-DL-GRP.mkv E3=- | dup E2 keep=Show.S01E02.WEB-DL-GRP.mkv del=Show - S01E02.mkv",
	"flat|own smaller copy":                                         "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"flat|own fills":                                                "E1=Show - S01E01.mkv E2=- E3=Show - S01E03.mkv",
	"S01 folder|foreign displaces":                                  "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|foreign smaller":                                    "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|foreign higher res":                                 "E1=Show - S01E01.mkv E2=Show - S01E02 - 720p.mkv E3=-",
	"S01 folder|foreign fills":                                      "E1=Show - S01E01.mkv E2=- E3=-",
	"S01 folder|foreign beside a gone file":                         "E1=Show - S01E01.mkv E2=- E3=-",
	"S01 folder|untitled displaces":                                 "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|untitled fills":                                     "E1=Show - S01E01.mkv E2=- E3=S01E03.mkv",
	"S01 folder|library show's file":                                "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|foreign already recorded":                           "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|foreign recorded, smaller":                          "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|untitled recorded, own bigger":                      "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|untitled recorded, own smaller":                     "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"S01 folder|foreign two-episode file":                           "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|own better resolution":                              "E1=Show - S01E01.mkv E2=Show - S01E02 - 1080p.mkv E3=- | dup E2 keep=Show - S01E02 - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"S01 folder|own bigger copy":                                    "E1=Show - S01E01.mkv E2=Show.S01E02.WEB-DL-GRP.mkv E3=- | dup E2 keep=Show.S01E02.WEB-DL-GRP.mkv del=Show - S01E02.mkv",
	"S01 folder|own smaller copy":                                   "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"S01 folder|own fills":                                          "E1=Show - S01E01.mkv E2=- E3=Show - S01E03.mkv",
	"category|foreign displaces":                                    "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|foreign smaller":                                      "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|foreign higher res":                                   "E1=Show - S01E01.mkv E2=Show - S01E02 - 720p.mkv E3=-",
	"category|foreign fills":                                        "E1=Show - S01E01.mkv E2=- E3=-",
	"category|foreign beside a gone file":                           "E1=Show - S01E01.mkv E2=- E3=-",
	"category|untitled displaces":                                   "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|untitled fills":                                       "E1=Show - S01E01.mkv E2=- E3=S01E03.mkv",
	"category|library show's file":                                  "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|foreign already recorded":                             "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|foreign recorded, smaller":                            "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|untitled recorded, own bigger":                        "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|untitled recorded, own smaller":                       "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"category|foreign two-episode file":                             "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|own better resolution":                                "E1=Show - S01E01.mkv E2=Show - S01E02 - 1080p.mkv E3=- | dup E2 keep=Show - S01E02 - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"category|own bigger copy":                                      "E1=Show - S01E01.mkv E2=Show.S01E02.WEB-DL-GRP.mkv E3=- | dup E2 keep=Show.S01E02.WEB-DL-GRP.mkv del=Show - S01E02.mkv",
	"category|own smaller copy":                                     "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|own fills":                                            "E1=Show - S01E01.mkv E2=- E3=Show - S01E03.mkv",
	"franchise S01, issue 56|foreign displaces":                     "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|foreign smaller":                       "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|foreign higher res":                    "E1=Show - S01E01.mkv E2=Show - S01E02 - 720p.mkv E3=-",
	"franchise S01, issue 56|foreign fills":                         "E1=Show - S01E01.mkv E2=- E3=-",
	"franchise S01, issue 56|foreign beside a gone file":            "E1=Show - S01E01.mkv E2=- E3=-",
	"franchise S01, issue 56|untitled displaces":                    "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|untitled fills":                        "E1=Show - S01E01.mkv E2=- E3=S01E03.mkv",
	"franchise S01, issue 56|library show's file":                   "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|foreign already recorded":              "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|foreign recorded, smaller":             "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|untitled recorded, own bigger":         "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|untitled recorded, own smaller":        "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"franchise S01, issue 56|foreign two-episode file":              "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|own better resolution":                 "E1=Show - S01E01.mkv E2=Show - S01E02 - 1080p.mkv E3=- | dup E2 keep=Show - S01E02 - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"franchise S01, issue 56|own bigger copy":                       "E1=Show - S01E01.mkv E2=Show.S01E02.WEB-DL-GRP.mkv E3=- | dup E2 keep=Show.S01E02.WEB-DL-GRP.mkv del=Show - S01E02.mkv",
	"franchise S01, issue 56|own smaller copy":                      "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"franchise S01, issue 56|own fills":                             "E1=Show - S01E01.mkv E2=- E3=Show - S01E03.mkv",
	"S01 in the TV folder, issue 56|foreign displaces":              "refused",
	"S01 in the TV folder, issue 56|foreign smaller":                "refused",
	"S01 in the TV folder, issue 56|foreign higher res":             "refused",
	"S01 in the TV folder, issue 56|foreign fills":                  "refused",
	"S01 in the TV folder, issue 56|foreign beside a gone file":     "refused",
	"S01 in the TV folder, issue 56|untitled displaces":             "refused",
	"S01 in the TV folder, issue 56|untitled fills":                 "refused",
	"S01 in the TV folder, issue 56|library show's file":            "refused",
	"S01 in the TV folder, issue 56|foreign already recorded":       "refused",
	"S01 in the TV folder, issue 56|foreign recorded, smaller":      "refused",
	"S01 in the TV folder, issue 56|untitled recorded, own bigger":  "refused",
	"S01 in the TV folder, issue 56|untitled recorded, own smaller": "refused",
	"S01 in the TV folder, issue 56|foreign two-episode file":       "refused",
	"S01 in the TV folder, issue 56|own better resolution":          "refused",
	"S01 in the TV folder, issue 56|own bigger copy":                "refused",
	"S01 in the TV folder, issue 56|own smaller copy":               "refused",
	"S01 in the TV folder, issue 56|own fills":                      "refused",
}

var foreignWas = map[string]string{
	"standard|foreign displaces":                             "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"standard|foreign higher res":                            "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other - 1080p.mkv E3=- | dup E2 keep=Beta - 1x02 - Other - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"standard|untitled displaces":                            "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"standard|library show's file":                           "E1=Show - S01E01.mkv E2=Beta - S01E02.mkv E3=- | dup E2 keep=Beta - S01E02.mkv del=Show - S01E02.mkv",
	"standard|foreign already recorded":                      "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"standard|untitled recorded, own bigger":                 "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=- | dup E2 keep=Show - S01E02.mkv del=S01E02.mkv",
	"standard|untitled recorded, own smaller":                "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"standard|foreign two-episode file":                      "E1=Show - S01E01.mkv E2=Beta - S01E02-E03 - Other.mkv E3=Beta - S01E02-E03 - Other.mkv | dup E2 keep=Beta - S01E02-E03 - Other.mkv del=Show - S01E02.mkv",
	"flat|foreign displaces":                                 "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"flat|foreign higher res":                                "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other - 1080p.mkv E3=- | dup E2 keep=Beta - 1x02 - Other - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"flat|untitled displaces":                                "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"flat|untitled fills":                                    "E1=Show - S01E01.mkv E2=- E3=S01E03.mkv",
	"flat|library show's file":                               "E1=Show - S01E01.mkv E2=Beta - S01E02.mkv E3=- | dup E2 keep=Beta - S01E02.mkv del=Show - S01E02.mkv",
	"flat|foreign already recorded":                          "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"flat|untitled recorded, own bigger":                     "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=- | dup E2 keep=Show - S01E02.mkv del=S01E02.mkv",
	"flat|untitled recorded, own smaller":                    "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"flat|foreign two-episode file":                          "E1=Show - S01E01.mkv E2=Beta - S01E02-E03 - Other.mkv E3=Beta - S01E02-E03 - Other.mkv | dup E2 keep=Beta - S01E02-E03 - Other.mkv del=Show - S01E02.mkv",
	"S01 folder|foreign displaces":                           "refused",
	"S01 folder|foreign smaller":                             "refused",
	"S01 folder|foreign higher res":                          "refused",
	"S01 folder|foreign fills":                               "refused",
	"S01 folder|foreign beside a gone file":                  "refused",
	"S01 folder|untitled displaces":                          "refused",
	"S01 folder|untitled fills":                              "refused",
	"S01 folder|library show's file":                         "refused",
	"S01 folder|foreign already recorded":                    "refused",
	"S01 folder|foreign recorded, smaller":                   "refused",
	"S01 folder|untitled recorded, own bigger":               "refused",
	"S01 folder|untitled recorded, own smaller":              "refused",
	"S01 folder|foreign two-episode file":                    "refused",
	"S01 folder|own better resolution":                       "refused",
	"S01 folder|own bigger copy":                             "refused",
	"S01 folder|own smaller copy":                            "refused",
	"S01 folder|own fills":                                   "refused",
	"category|untitled fills":                                "E1=Show - S01E01.mkv E2=- E3=-",
	"category|foreign already recorded":                      "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=-",
	"category|foreign recorded, smaller":                     "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=-",
	"category|untitled recorded, own bigger":                 "E1=Show - S01E01.mkv E2=S01E02.mkv E3=-",
	"category|own better resolution":                         "E1=Show - S01E01.mkv E2=Show - S01E02 - 720p.mkv E3=-",
	"category|own bigger copy":                               "E1=Show - S01E01.mkv E2=Show - S01E02.mkv E3=-",
	"category|own fills":                                     "E1=Show - S01E01.mkv E2=- E3=-",
	"franchise S01, issue 56|foreign displaces":              "refused",
	"franchise S01, issue 56|foreign smaller":                "refused",
	"franchise S01, issue 56|foreign higher res":             "refused",
	"franchise S01, issue 56|foreign fills":                  "refused",
	"franchise S01, issue 56|foreign beside a gone file":     "refused",
	"franchise S01, issue 56|untitled displaces":             "refused",
	"franchise S01, issue 56|untitled fills":                 "refused",
	"franchise S01, issue 56|library show's file":            "refused",
	"franchise S01, issue 56|foreign already recorded":       "refused",
	"franchise S01, issue 56|foreign recorded, smaller":      "refused",
	"franchise S01, issue 56|untitled recorded, own bigger":  "refused",
	"franchise S01, issue 56|untitled recorded, own smaller": "refused",
	"franchise S01, issue 56|foreign two-episode file":       "refused",
	"franchise S01, issue 56|own better resolution":          "refused",
	"franchise S01, issue 56|own bigger copy":                "refused",
	"franchise S01, issue 56|own smaller copy":               "refused",
	"franchise S01, issue 56|own fills":                      "refused",
}

var foreignMain = map[string]string{
	"standard|foreign displaces":                             "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"standard|foreign higher res":                            "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other - 1080p.mkv E3=- | dup E2 keep=Beta - 1x02 - Other - 1080p.mkv del=Show - S01E02 - 720p.mkv",
	"standard|untitled displaces":                            "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"standard|foreign already recorded":                      "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"standard|untitled recorded, own smaller":                "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"standard|foreign two-episode file":                      "E1=Show - S01E01.mkv E2=Beta - S01E02-E03 - Other.mkv E3=Beta - S01E02-E03 - Other.mkv | dup E2 keep=Beta - S01E02-E03 - Other.mkv del=Show - S01E02.mkv",
	"flat|foreign displaces":                                 "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"flat|foreign already recorded":                          "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"flat|untitled recorded, own smaller":                    "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"flat|foreign two-episode file":                          "E1=Show - S01E01.mkv E2=Beta - S01E02-E03 - Other.mkv E3=Beta - S01E02-E03 - Other.mkv | dup E2 keep=Beta - S01E02-E03 - Other.mkv del=Show - S01E02.mkv",
	"S01 folder|untitled displaces":                          "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"S01 folder|foreign already recorded":                    "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"S01 folder|untitled recorded, own smaller":              "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"category|untitled displaces":                            "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"category|foreign already recorded":                      "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"category|untitled recorded, own smaller":                "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"franchise S01, issue 56|untitled displaces":             "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
	"franchise S01, issue 56|foreign already recorded":       "E1=Show - S01E01.mkv E2=Beta - 1x02 - Other.mkv E3=- | dup E2 keep=Beta - 1x02 - Other.mkv del=Show - S01E02.mkv",
	"franchise S01, issue 56|untitled recorded, own smaller": "E1=Show - S01E01.mkv E2=S01E02.mkv E3=- | dup E2 keep=S01E02.mkv del=Show - S01E02.mkv",
}

func TestRescanForeignFileLayouts(t *testing.T) {
	for _, l := range foreignLayouts {
		for _, sc := range foreignScenarios {
			l, sc := l, sc
			t.Run(l.name+"|"+sc.name, func(t *testing.T) {
				got := foreignOutcome(t, l.dir, sc)
				key := l.name + "|" + sc.name
				want := foreignWant[key]
				if got != want {
					t.Errorf("\n got: %s\nwant: %s\n was: %s\nmain: %s", got, want,
						orSame(foreignWas[key], want), orSame(foreignMain[key], want))
				}
			})
		}
	}
}

func orSame(s, want string) string {
	if s == "" {
		return want
	}
	return s
}

func foreignOutcome(t *testing.T, dir string, sc foreignScenario) string {
	t.Helper()
	h := newLifecycleHarness(t)
	tv := h.c.imp.TVRoot()
	mustExec(t, h.c, `UPDATE series SET year = 2010 WHERE id = ?`, h.seriesID)
	mustExec(t, h.c, `INSERT OR IGNORE INTO episodes (series_id, season_number, episode_number, air_date, monitored) VALUES (?, 1, 3, '2020-01-15', 1)`, h.seriesID)
	if sc.library {
		mustExec(t, h.c, `INSERT INTO series (tmdb_id, title, year, monitored) VALUES (99, 'Beta', 2012, 1)`)
	}
	files := append([]foreignFile{{name: "Show - S01E01.mkv", mb: 60, ep: 1}}, sc.files...)
	var all []string
	for _, f := range files {
		p := filepath.Join(tv, filepath.FromSlash(dir), f.name)
		if !f.gone {
			sparseFile(t, p, f.mb<<20)
			all = append(all, p)
		}
		if f.ep > 0 {
			rvSet(t, h, 1, f.ep, p)
		}
	}

	var out []string
	if err := h.c.RescanSeries(h.ctx, h.seriesID); err != nil {
		out = append(out, "refused")
	} else {
		for ep := 1; ep <= 3; ep++ {
			name := "-"
			if p := h.c.series.CurrentEpisodeFile(h.ctx, h.seriesID, 1, ep).Path; p != "" {
				name = filepath.Base(p)
			}
			out = append(out, "E"+string(rune('0'+ep))+"="+name)
		}
	}
	dupes, _ := h.c.SeriesDuplicates(h.ctx, h.seriesID)
	for _, d := range dupes {
		var del []string
		for _, e := range d.Extras {
			del = append(del, e.Filename)
		}
		sort.Strings(del)
		out = append(out, "| dup E"+string(rune('0'+d.Episode))+" keep="+d.Keeping.Filename+" del="+strings.Join(del, ","))
	}
	for _, p := range all {
		if _, err := os.Stat(p); err != nil {
			out = append(out, "| LOST "+filepath.Base(p))
		}
	}
	return strings.Join(out, " ")
}
