package automation

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// seriesPlacement is where show s's episodes go: the importer (its TV folder fixed to the
// folder the show's folder is in, so a show in the Anime folder never grows a second copy
// in the default one; Using(ctx), so a hand import running as a job hears its own copies)
// and the show folder's name ("" = a new folder named by the scheme). It refuses — with a
// *series.ShowFolderError — when that isn't safe:
//
//   - the show has files, but none in a folder of its own (all in a folder named like a
//     season, or one other shows' files are in), so where it lives can't be told;
//   - the folder it would go to is named like a season folder, is a TV folder, or holds
//     another show's files.
//
// Every path that places episode files, and every rescan or rename that walks the show's
// folder, goes through this. Importing into a shared folder merged shows' season folders
// and let their episodes overwrite each other (#56); walking one let a rescan claim
// another show's files.
func (c *Coordinator) seriesPlacement(ctx context.Context, s series.Series) (*library.Importer, string, error) {
	info := c.series.ShowFolderOf(ctx, s.ID)
	if info.Unknown {
		return nil, "", &series.ShowFolderError{Title: s.Title, Why: info.Why}
	}
	imp := c.imp.InTV(c.series.ImportDirOf(ctx, s, info)).Using(ctx)
	folder := ""
	if info.Path != "" {
		folder = filepath.Base(info.Path)
	}
	if why := c.series.UnsafeImportFolder(ctx, s, imp.SeriesFolderPath(folder, s.Title, s.Year)); why != "" {
		return nil, "", &series.ShowFolderError{Title: s.Title, Why: why}
	}
	return imp, folder, nil
}

// placementFor is where show s's episodes go, one season at a time. For an ordinary show
// every season goes where seriesPlacement says. For a #56-shaped show (its folder can't be
// told) an episode goes into the one folder its season's recorded files are already in
// (series.SeasonDir), and a season without one is refused. shape56 says which.
func (c *Coordinator) placementFor(ctx context.Context, s series.Series) (place func(season int) (*library.Importer, string, error), shape56 bool) {
	info := c.series.ShowFolderOf(ctx, s.ID)
	if !info.Shape56 {
		imp, folder, err := c.seriesPlacement(ctx, s)
		return func(int) (*library.Importer, string, error) { return imp, folder, err }, false
	}
	type placed struct {
		imp *library.Importer
		err error
	}
	cache := map[int]placed{}
	return func(season int) (*library.Importer, string, error) {
		if p, ok := cache[season]; ok {
			return p.imp, "", p.err
		}
		var p placed
		if dir, why := c.series.SeasonDir(ctx, s.ID, season); why != "" {
			p.err = &series.ShowFolderError{Title: s.Title, Why: info.Why + " (" + why + ")"}
		} else {
			p.imp = c.imp.InTV(c.series.TVRootOf(dir)).IntoSeasonDir(dir).Using(ctx)
		}
		cache[season] = p
		return p.imp, "", p.err
	}, true
}

// placementCheck is the check before importing the download called name for show s: an
// error holds it for Review. A #56-shaped show takes one episode or one season of a season
// it already has a folder for; anything wider is refused. It reads the name only; the
// import paths, which have the download, use placementCheckIn.
func (c *Coordinator) placementCheck(ctx context.Context, s series.Series, name string) error {
	return c.placementCheckIn(ctx, s, name, "")
}

// placementCheckIn is placementCheck for the download called name at contentPath. For a
// #56-shaped show whose download name says neither one episode nor one season (a pack
// named "Show.Stagione.2…", which the parser doesn't read), the video files inside decide:
// when every one the import would take is named as an episode of the same season, that
// season's folder rule applies, unchanged. Anything else is refused, as before.
func (c *Coordinator) placementCheckIn(ctx context.Context, s series.Series, name, contentPath string) error {
	place, shape56 := c.placementFor(ctx, s)
	if !shape56 {
		_, _, err := place(0)
		return err
	}
	rel := parser.Parse(name)
	switch {
	case rel.Kind() == parser.KindEpisode && len(rel.Episodes) > 0, rel.Kind() == parser.KindSeasonPack && rel.Season > 0:
		_, _, err := place(rel.Season)
		return err
	}
	why := ""
	if contentPath != "" {
		season, w := filesSeason(contentPath)
		if season > 0 {
			_, _, err := place(season)
			return err
		}
		why = ": " + w
	}
	info := c.series.ShowFolderOf(ctx, s.ID)
	return &series.ShowFolderError{Title: s.Title, Why: fmt.Sprintf("%s (this download isn't one season's episodes%s)", info.Why, why)}
}

// filesSeason is the one season every video file in the download at contentPath is an
// episode of, read from each file's own name: the files the import takes (FindVideos, so
// samples and small extras are skipped). It is 0, with why, when there's no such season:
// no video files, a file whose name doesn't say its season and episode, or files of more
// than one season.
func filesSeason(contentPath string) (int, string) {
	vids, err := library.FindVideos(contentPath)
	if err != nil || len(vids) == 0 {
		return 0, "no video files were found in it"
	}
	seen := map[int]bool{}
	var seasons []int
	for _, v := range vids {
		p := parser.Parse(filepath.Base(v.Path))
		if p.Kind() != parser.KindEpisode || len(p.Episodes) == 0 || p.Season <= 0 {
			return 0, fmt.Sprintf("its file %s doesn't say which season and episode it is", filepath.Base(v.Path))
		}
		if !seen[p.Season] {
			seen[p.Season] = true
			seasons = append(seasons, p.Season)
		}
	}
	if len(seasons) > 1 {
		sort.Ints(seasons)
		names := make([]string, len(seasons))
		for i, n := range seasons {
			names[i] = strconv.Itoa(n)
		}
		return 0, fmt.Sprintf("its files are from seasons %s and %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
	}
	return seasons[0], ""
}
