package automation

import (
	"path/filepath"
	"testing"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Filling an episode with no file, the show's own file goes first, then a title-less one,
// then one named as something else, and a file named otherwise that only the on-disk "1x8"
// rule reads last — whichever is the better copy.
func TestKeptEpisodeFileFillsWithTheShowsOwnFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Show (2010)", "Season 1")
	mk := func(name string, mb int64) library.FoundVideo {
		p := filepath.Join(dir, name)
		sparseFile(t, p, 1<<20)
		return library.FoundVideo{Path: p, Size: mb << 20}
	}
	show := series.Series{Title: "Show", Year: 2010}
	own := mk("Show - S01E03 - 720p.mkv", 60)
	own1x8 := mk("Show - 1x3 - Title.mkv", 60)
	untitled := mk("S01E03 - 1080p.mkv", 60)
	foreign := mk("Beta - S01E03 - 2160p.mkv", 90)
	foreign1x03 := mk("Beta - 1x03 - Other.mkv", 90)
	gone := filepath.Join(dir, "Show - S01E03 - gone.mkv")
	for _, c := range []struct {
		name     string
		files    []library.FoundVideo
		recorded string
		want     library.FoundVideo
	}{
		{"own beside a better foreign copy", []library.FoundVideo{foreign, own}, "", own},
		{"own 1x8 beside a foreign copy", []library.FoundVideo{foreign1x03, own1x8}, "", own1x8},
		{"title-less beside a better foreign copy", []library.FoundVideo{foreign, untitled}, "", untitled},
		{"own beside a title-less copy, the record gone", []library.FoundVideo{untitled, own}, gone, own},
		{"only foreign copies: the better", []library.FoundVideo{foreign1x03, foreign}, "", foreign},
	} {
		got, _, ok := keptEpisodeFile(c.files, c.recorded, show)
		if !ok || got.Path != c.want.Path {
			t.Errorf("%s: kept %q ok=%v, want %q", c.name, filepath.Base(got.Path), ok, filepath.Base(c.want.Path))
		}
	}
}
