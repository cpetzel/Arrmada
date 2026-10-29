package automation

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// What the series import sweep knows that nobody else could see: packs whose files keep
// failing to place (disk full, a folder it can't write) or whose download folder it can't
// read (not mounted, the wrong owner), and finished TV downloads sitting
// in a category it never imports from. Both used to be log lines only; the Needs-you feed
// reads them from here. In memory on purpose: a restart retries from scratch, and the
// next sweep (30 seconds) finds the same downloads again.

// seriesImportFail is one series download whose files keep failing to place.
type seriesImportFail struct {
	name    string
	sweeps  int
	since   time.Time
	lastErr string
	// unreadable: the last sweep couldn't read the download's folder (as opposed to files
	// it couldn't place). Such a record waits unreadableShowAfter before Needs you shows it.
	unreadable bool
}

// importNotes holds those findings. Its own lock: the sweep writes, the feed reads.
type importNotes struct {
	mu       sync.Mutex
	failing  map[string]*seriesImportFail // by lowercased hash
	wrongCat []download.Item              // the last sweep's misfiled TV downloads
	loggedAt map[string]time.Time         // when each misfiled download was last logged
	// unreadLogged is when each download whose folder couldn't be read was last logged, by
	// lowercased hash: kept apart from failing, since an imported pack's isn't a failure.
	unreadLogged map[string]time.Time
}

// wrongCatLogEvery throttles the "wrong category" warning to once per download per hour;
// the sweep runs every 30 seconds. The unreadable-folder warning uses the same hour.
const wrongCatLogEvery = time.Hour

// unreadableShowAfter is how long a download's folder must have been unreadable before
// Needs you lists it. A share that drops for a minute, or a container restarting beside
// Arrmada, clears up by itself; a missing mount or path mapping is still there after five.
const unreadableShowAfter = 5 * time.Minute

// noteSeriesImportFail counts another sweep in which failed of a download's files couldn't
// be placed.
func (c *Coordinator) noteSeriesImportFail(hash, name string, failed int) {
	n := &c.importNotes
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.failing == nil {
		n.failing = map[string]*seriesImportFail{}
	}
	h := strings.ToLower(hash)
	f := n.failing[h]
	if f == nil {
		f = &seriesImportFail{since: c.clock()}
		n.failing[h] = f
	}
	f.name = name
	f.sweeps++
	f.unreadable = false
	f.lastErr = pluralCount(failed, "file couldn't be placed in the library", "files couldn't be placed in the library") +
		" — the log names the folder and the error"
}

// noteSeriesImportUnreadable notes a sweep in which a download's folder couldn't be read
// (importSeriesInto's scanErr), and logs it at most once an hour per download: the sweep
// runs every 30 seconds, and a folder that isn't mounted stays that way until someone
// mounts it.
//
// A download not yet imported counts another failing sweep in the failing map, so Needs
// you lists it once, with the folder and the error, after unreadableShowAfter. One already
// imported (a season pack re-read to fill gaps the season still has) only logs: what it
// brought is in the library, and the gaps aren't something it failed to do.
func (c *Coordinator) noteSeriesImportUnreadable(it download.Item, seriesTitle string, scanErr error, imported bool) {
	n := &c.importNotes
	now := c.clock()
	h := strings.ToLower(it.Hash)
	n.mu.Lock()
	if !imported {
		if n.failing == nil {
			n.failing = map[string]*seriesImportFail{}
		}
		f := n.failing[h]
		if f == nil {
			f = &seriesImportFail{since: now}
			n.failing[h] = f
		}
		f.name = it.Name
		f.sweeps++
		f.unreadable = true
		f.lastErr = unreadableFolder(it.ContentPath, scanErr)
	}
	if n.unreadLogged == nil {
		n.unreadLogged = map[string]time.Time{}
	}
	last, ok := n.unreadLogged[h]
	logNow := !ok || now.Sub(last) >= wrongCatLogEvery
	if logNow {
		n.unreadLogged[h] = now
	}
	n.mu.Unlock()
	switch {
	case !logNow:
	case imported:
		c.log.Warn("series import: can't read an imported pack's folder to fill the season's gaps — skipping it, will look again next sweep",
			"series", seriesTitle, "release", it.Name, "content_path", it.ContentPath, "err", scanErr)
	default:
		c.log.Warn("series import: can't read the download folder — not blocklisting, will retry every sweep",
			"series", seriesTitle, "release", it.Name, "content_path", it.ContentPath, "err", scanErr)
	}
}

// unreadableFolder is what a person is told about a download folder Arrmada couldn't read:
// the folder, the error (which names the part that failed), and where the fix usually is.
func unreadableFolder(path string, err error) string {
	if errors.Is(err, library.ErrChanged) {
		return "The download folder " + path + " keeps changing while Arrmada reads it (" + err.Error() +
			") — something is moving or rechecking it; it's tried again every sweep"
	}
	return "Arrmada can't read the download folder " + path + " (" + err.Error() +
		") — check it's mounted in the Arrmada container and readable by it, or add a path mapping"
}

// clearSeriesImportFail forgets a download once a sweep has handled it.
func (c *Coordinator) clearSeriesImportFail(hash string) {
	n := &c.importNotes
	n.mu.Lock()
	delete(n.failing, strings.ToLower(hash))
	delete(n.unreadLogged, strings.ToLower(hash))
	n.mu.Unlock()
}

// pruneSeriesImportFails forgets downloads no longer in the completed list (removed, or
// sent to review), so the map only ever holds what's in the client.
func (c *Coordinator) pruneSeriesImportFails(active map[string]bool) {
	n := &c.importNotes
	n.mu.Lock()
	defer n.mu.Unlock()
	for h := range n.failing {
		if !active[h] {
			delete(n.failing, h)
		}
	}
	for h := range n.unreadLogged {
		if !active[h] {
			delete(n.unreadLogged, h)
		}
	}
}

// SeriesImportFailures is every series download whose files have failed to place on one
// sweep or more and not yet succeeded, in the shape the movie importer reports its own
// (library.Manager.Failures). Attempts counts sweeps; the next one is the next sweep. A
// download whose folder can't be read is left out until it has been so for
// unreadableShowAfter: a brief blip isn't worth a row.
func (c *Coordinator) SeriesImportFailures() []library.FailureInfo {
	n := &c.importNotes
	now := c.clock()
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]library.FailureInfo, 0, len(n.failing))
	for h, f := range n.failing {
		if f.unreadable && now.Sub(f.since) < unreadableShowAfter {
			continue
		}
		out = append(out, library.FailureInfo{Hash: h, Name: f.name, Attempts: f.sweeps, Since: f.since, LastErr: f.lastErr})
	}
	return out
}

// wrongCategory is the completed TV downloads in completed that aren't in the series
// category but match a library series by name: they will never import on their own.
func wrongCategory(completed []download.Item, match func(parser.Release) (series.Series, bool, []series.Series)) []download.Item {
	var out []download.Item
	for _, it := range completed {
		if it.Category == seriesCategory || !it.Complete() {
			continue
		}
		p := parser.Parse(it.Name)
		if !p.IsTV() {
			continue
		}
		if _, ok, cands := match(p); ok || len(cands) > 0 {
			out = append(out, it)
		}
	}
	return out
}

// noteWrongCategory keeps the sweep's misfiled downloads for the feed and logs each one,
// at most once an hour.
func (c *Coordinator) noteWrongCategory(items []download.Item) {
	n := &c.importNotes
	now := c.clock()
	n.mu.Lock()
	n.wrongCat = append([]download.Item(nil), items...)
	if n.loggedAt == nil {
		n.loggedAt = map[string]time.Time{}
	}
	var logNow []download.Item
	seen := map[string]bool{}
	for _, it := range items {
		h := strings.ToLower(it.Hash)
		seen[h] = true
		if last, ok := n.loggedAt[h]; ok && now.Sub(last) < wrongCatLogEvery {
			continue
		}
		n.loggedAt[h] = now
		logNow = append(logNow, it)
	}
	for h := range n.loggedAt {
		if !seen[h] {
			delete(n.loggedAt, h)
		}
	}
	n.mu.Unlock()
	for _, it := range logNow {
		c.log.Warn("series import: a completed TV download is in the wrong category — it won't import; re-grab via Arrmada or set its qBittorrent category to "+seriesCategory,
			"release", it.Name, "category", it.Category)
	}
}

// WrongCategoryDownloads is the finished TV downloads the last series import sweep found
// in a category it doesn't import from, matching a series in the library. A copy, sorted
// by name.
func (c *Coordinator) WrongCategoryDownloads() []download.Item {
	n := &c.importNotes
	n.mu.Lock()
	out := append([]download.Item(nil), n.wrongCat...)
	n.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func pluralCount(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}
