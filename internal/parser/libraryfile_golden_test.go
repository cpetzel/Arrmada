package parser

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// testdata/parse_corpus.tsv is every release and file name the repo's tests use (pulled out
// of every _test.go file) plus common release names and the #81 library forms, each with a
// fingerprint of what Parse read from it at main 931de3b3, before the single-digit NxE
// change. Parse itself must read every one exactly as it did; ParseLibraryFile must too,
// except for the names listed below, which are the ones the #81 rule exists to read.
//
// go test ./internal/parser -run TestParseCorpusUnchanged -update-corpus rewrites the
// fingerprints from the current Parse — only ever on purpose.
var updateCorpus = flag.Bool("update-corpus", false, "rewrite testdata/parse_corpus.tsv from the current Parse")

const corpusFile = "testdata/parse_corpus.tsv"

func fingerprint(r Release) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", r)))
	return hex.EncodeToString(sum[:8])
}

type corpusEntry struct{ hash, name string }

func readCorpus(t *testing.T) []corpusEntry {
	t.Helper()
	f, err := os.Open(corpusFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []corpusEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r") // a Windows checkout's CRLF
		if line == "" {
			continue
		}
		h, name, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad corpus line %q", line)
		}
		out = append(out, corpusEntry{h, name})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) < 4000 {
		t.Fatalf("corpus has %d names, want the full set", len(out))
	}
	return out
}

func TestParseCorpusUnchanged(t *testing.T) {
	entries := readCorpus(t)
	if *updateCorpus {
		var b strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&b, "%s\t%s\n", fingerprint(Parse(e.name)), e.name)
		}
		if err := os.WriteFile(corpusFile, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, e := range entries {
		if got := fingerprint(Parse(e.name)); got != e.hash {
			t.Errorf("Parse(%q) changed: %#v", e.name, Parse(e.name))
		}
	}
}

// libraryReadsNoFolder are the corpus names ParseLibraryFile reads differently from Parse
// for a file in no season folder: each "Title - NxE - …" set off by dashes.
var libraryReadsNoFolder = map[string]string{
	"11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv": "1x8",
	"11.22.63 - 1x1- The Rabbit Hole [HDTVMux1080p Ita 5.1] NovaRip.mkv":     "1x1",
	"11.22.63 - 1x2- The Kill Floor [HDTVMux1080p Ita 5.1] NovaRip.mkv":      "1x2",
	"Show - 1x8 - Title.mkv":                                     "1x8",
	"Show - 1x8- Title.mkv":                                      "1x8",
	"Show-1x8-Title.mkv":                                         "1x8",
	"Show - 2x3.mkv":                                             "2x3",
	"Show - 1x8-1x9 - Title.mkv":                                 "1x8,9",
	"Show - 1x8 & 1x9 - Title.mkv":                               "1x8,9",
	"Show - 1x7-1x9 - Title.mkv":                                 "1x7,8,9",
	"Show - 1x8 - 16x9 Edition.mkv":                              "1x8",
	"Show - 12x3 - Title.mkv":                                    "12x3",
	"Show - 1x8 - Title [1080p].mkv":                             "1x8",
	"Show_-_1x8_-_Title.mkv":                                     "1x8",
	"[Group] Show - 1x8 - Title [1080p].mkv":                     "1x8",
	"Show (2016) - 1x8 - Title.mkv":                              "1x8",
	"Show - [1x2] - Title.mkv":                                   "",
	"La Casa di Carta - 1x3 - Episodio 3 [WEBDLMux1080p Ita Eng].mkv": "1x3",
	"Gomorra - 2x5 - Titolo [HDTVMux720p Ita].mkv":                    "2x5",
}

// libraryReadsSeason1 are the corpus names ParseLibraryFile reads differently from Parse
// for a file in a "Season 1" folder: every season-1 token, dashes or not, outside brackets
// and before the release tags.
var libraryReadsSeason1 = map[string]string{
	"11.22.63 - 1x8- The Day in Question [HDTVMux1080p Ita 5.1] NovaRip.mkv": "1x8",
	"11.22.63 - 1x1- The Rabbit Hole [HDTVMux1080p Ita 5.1] NovaRip.mkv":     "1x1",
	"11.22.63 - 1x2- The Kill Floor [HDTVMux1080p Ita 5.1] NovaRip.mkv":      "1x2",
	"Show - 1x8 - Title.mkv":                                     "1x8",
	"Show - 1x8- Title.mkv":                                      "1x8",
	"Show-1x8-Title.mkv":                                         "1x8",
	"Show 1x8 Title.mkv":                                         "1x8",
	"Show.1x8.Title.mkv":                                         "1x8",
	"Show - 1x8-1x9 - Title.mkv":                                 "1x8,9",
	"Show - 1x8 & 1x9 - Title.mkv":                               "1x8,9",
	"Show - 1x7-1x9 - Title.mkv":                                 "1x7,8,9",
	"Show - 1x8 - 16x9 Edition.mkv":                              "1x8",
	"Show - 1x8 - Title [1080p].mkv":                             "1x8",
	"Show_-_1x8_-_Title.mkv":                                     "1x8",
	"[Group] Show - 1x8 - Title [1080p].mkv":                     "1x8",
	"Show (2016) - 1x8 - Title.mkv":                              "1x8",
	"1x8 - Title.mkv":                                            "1x8",
	"- 1x8 - Title.mkv":                                          "1x8",
	"La Casa di Carta - 1x3 - Episodio 3 [WEBDLMux1080p Ita Eng].mkv": "1x3",
}

func describeNxE(r Release) string {
	if !r.SeasonExplicit || len(r.Episodes) == 0 {
		return ""
	}
	eps := make([]string, len(r.Episodes))
	for i, e := range r.Episodes {
		eps[i] = fmt.Sprint(e)
	}
	return fmt.Sprintf("%dx%s", r.Season, strings.Join(eps, ","))
}

// Over the whole corpus, ParseLibraryFile reads what Parse reads except for the names the
// rule targets — in no season folder, in a Season 1 folder, in Specials, and in a Season 2
// folder (which must disagree with every season-1 token and so change nothing but the
// season-2 ones).
func TestParseLibraryFileCorpusDiff(t *testing.T) {
	entries := readCorpus(t)
	type ctx struct {
		label  string
		season int
		in     bool
		want   map[string]string
	}
	season2 := map[string]string{
		"Show - 2x3.mkv": "2x3",
		"Gomorra - 2x5 - Titolo [HDTVMux720p Ita].mkv": "2x5",
		// Not a file name (a calendar UID from the iCal tests), but its token is the
		// season-2 episode it names; in a Season 2 folder that is what it would be.
		"UID:arrmada-ep-11-2x5@arrmada.example.com": "2x5",
	}
	specials := map[string]string{
		"Show - 0x1 - Special.mkv": "0x1",
	}
	for _, c := range []ctx{
		{"no season folder", 0, false, libraryReadsNoFolder},
		{"Season 1 folder", 1, true, libraryReadsSeason1},
		{"Season 2 folder", 2, true, season2},
		{"Specials folder", 0, true, specials},
	} {
		t.Run(c.label, func(t *testing.T) {
			var diffs []string
			seen := map[string]bool{}
			for _, e := range entries {
				got := ParseLibraryFile(e.name, c.season, c.in)
				if fingerprint(got) == e.hash {
					if w, ok := c.want[e.name]; ok && w != "" {
						t.Errorf("%q: want it read as %s, Parse's reading kept", e.name, w)
					}
					continue
				}
				seen[e.name] = true
				diffs = append(diffs, fmt.Sprintf("%s → %s", e.name, describeNxE(got)))
				w, ok := c.want[e.name]
				if !ok || w == "" {
					t.Errorf("%q: read differently from Parse (%s), and the rule doesn't target it", e.name, describeNxE(got))
					continue
				}
				if d := describeNxE(got); d != w {
					t.Errorf("%q: read as %q, want %q", e.name, d, w)
				}
			}
			for name, w := range c.want {
				if w != "" && !seen[name] {
					found := false
					for _, e := range entries {
						found = found || e.name == name
					}
					if !found {
						t.Errorf("%q isn't in the corpus", name)
					}
				}
			}
			sort.Strings(diffs)
			t.Logf("%s: %d of %d names read differently:\n  %s", c.label, len(diffs), len(entries), strings.Join(diffs, "\n  "))
		})
	}
}
