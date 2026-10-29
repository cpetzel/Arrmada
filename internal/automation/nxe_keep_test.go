package automation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
)

// #81: a file only the on-disk "1x8" rule reads fills an episode nothing else holds; it never
// takes the place of the recorded file, nor of a file read by its name — so the duplicate
// check never offers the recorded file for deletion in its favour.
func TestKeptEpisodeFileNeverPrefersAnOnDiskOnlyRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Dupe Show (2012)", "Season 1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mk := func(name string, size int64) library.FoundVideo {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return library.FoundVideo{Path: p, Size: size}
	}
	old := mk("Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv", 100)
	other := mk("Dupe Show - S01E08 - Other [720p].mkv", 50)
	newer := mk("Dupe Show - 1x8 - Title [1080p].mkv", 900)
	newer2 := mk("Dupe Show - 1x8 - Title [2160p].mkv", 1900)
	if !library.ReadOnlyOnDisk(newer.Path) || library.ReadOnlyOnDisk(old.Path) {
		t.Fatal("ReadOnlyOnDisk misjudges the fixture")
	}

	for _, c := range []struct {
		name     string
		files    []library.FoundVideo
		recorded string
		want     string
		ok       bool
	}{
		{"recorded file beside a better 1x8 copy", []library.FoundVideo{old, newer}, old.Path, old.Path, true},
		{"no record: a name-read file beats a 1x8 copy", []library.FoundVideo{newer, old}, "", old.Path, true},
		{"only the 1x8 copy, recorded file elsewhere and there", []library.FoundVideo{newer}, other.Path, "", false},
		{"only the 1x8 copy, nothing recorded", []library.FoundVideo{newer}, "", newer.Path, true},
		{"only the 1x8 copy, the recorded file gone", []library.FoundVideo{newer}, filepath.Join(dir, "gone.mkv"), newer.Path, true},
		// Recorded already (it filled the episode earlier), a name-read copy still wins.
		{"the recorded file is the 1x8 copy, beside a name-read copy", []library.FoundVideo{newer, other}, newer.Path, other.Path, true},
		// Only on-disk reads, one of them recorded: the recorded one stays.
		{"the recorded 1x8 copy beside another 1x8 copy", []library.FoundVideo{newer2, newer}, newer.Path, newer.Path, true},
	} {
		got, ok := keptEpisodeFile(c.files, c.recorded)
		if ok != c.ok || (ok && got.Path != c.want) {
			t.Errorf("%s: kept %q ok=%v, want %q ok=%v", c.name, filepath.Base(got.Path), ok, filepath.Base(c.want), c.ok)
		}
	}
}

// The duplicate check never offers a file for deletion when the only evidence that it's the
// same episode is the on-disk "1x8" reading of either file.
func TestSeriesDuplicatesNeverOfferOnAnOnDiskOnlyRead(t *testing.T) {
	const dir = "Dupe Show (2012)/Season 1/"
	tc := nxeLayoutCase{name: "dupes", title: "Dupe Show", year: 2012, seasons: map[int]int{1: 8}}
	scan := func(t *testing.T, files ...string) (*nxeEnv, int64) {
		t.Helper()
		tc.files = files
		e := newNxeEnv(t, tc)
		id := e.scanned(t, tc)
		if id == 0 {
			t.Fatal("scan added nothing")
		}
		return e, id
	}
	dupes := func(t *testing.T, e *nxeEnv, id int64) []DuplicateEpisodeFile {
		t.Helper()
		d, err := e.c.SeriesDuplicates(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	// A 1x8 copy beside the S01E08 file: not offered.
	if e, id := scan(t, dir+"Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv", dir+"Dupe Show - 1x8 - Title [1080p].mkv"); len(dupes(t, e, id)) != 0 {
		t.Errorf("1x8 beside S01E08: offered %+v", dupes(t, e, id))
	}
	// Two 1x8 copies: not offered.
	if e, id := scan(t, dir+"Dupe Show - 1x8 - Title [720p].mkv", dir+"Dupe Show - 1x8 - Title [1080p].mkv"); len(dupes(t, e, id)) != 0 {
		t.Errorf("two 1x8 copies: offered %+v", dupes(t, e, id))
	}
	// Control: two name-read copies are offered, as before.
	if e, id := scan(t, dir+"Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv", dir+"Dupe Show - S01E08 - Title [1080p].mkv"); len(dupes(t, e, id)) != 1 {
		t.Errorf("two S01E08 copies: offered %+v, want one", dupes(t, e, id))
	}

	// A 1x8 file filled the episode; then a name-read copy arrives. The rescan takes the
	// name-read copy, and neither is offered for deletion.
	e, id := scan(t, dir+"Dupe Show - 1x8 - Title [1080p].mkv")
	named := filepath.Join(e.root, filepath.FromSlash(dir+"Dupe.Show.S01E08.720p.HDTV.x264-GRP.mkv"))
	if err := os.WriteFile(named, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(named, 300<<20); err != nil {
		t.Fatal(err)
	}
	if err := e.c.RescanSeries(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.c.series.EpisodeFilePath(e.ctx, id, 1, 8); filepath.Clean(p) != filepath.Clean(named) {
		t.Errorf("after rescan S01E08 = %q, want the name-read copy", p)
	}
	if d := dupes(t, e, id); len(d) != 0 {
		t.Errorf("after rescan: offered %+v", d)
	}
}
