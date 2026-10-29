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
		// Recorded already (it filled the episode earlier): it competes as any file does.
		{"the recorded file is the 1x8 copy", []library.FoundVideo{newer, other}, newer.Path, newer.Path, true},
	} {
		got, ok := keptEpisodeFile(c.files, c.recorded)
		if ok != c.ok || (ok && got.Path != c.want) {
			t.Errorf("%s: kept %q ok=%v, want %q ok=%v", c.name, filepath.Base(got.Path), ok, filepath.Base(c.want), c.ok)
		}
	}
}
