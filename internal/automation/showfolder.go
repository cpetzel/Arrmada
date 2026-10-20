package automation

import (
	"context"
	"fmt"
	"path/filepath"

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
// it already has a folder for; anything wider is refused.
func (c *Coordinator) placementCheck(ctx context.Context, s series.Series, name string) error {
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
	info := c.series.ShowFolderOf(ctx, s.ID)
	return &series.ShowFolderError{Title: s.Title, Why: fmt.Sprintf("%s (this download isn't one season's episodes)", info.Why)}
}
