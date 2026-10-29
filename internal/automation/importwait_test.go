package automation

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The series import used to read a download folder it couldn't open as a release with no
// video in it. With qBittorrent's incomplete folder on, a torrent's content path names that
// folder until the move to the save folder is done, and Arrmada doesn't mount it: the
// season pack was blocklisted as junk, its grab failed, and the next search grabbed another
// copy of the same season (reported in #94).

const waitPack = "Show.S01.1080p.WEB-DL-GRP"

// setTorrent changes a fake torrent's state and content path, as qBittorrent does when a
// move finishes.
func (h *lifecycleHarness) setTorrent(hash, state, contentPath string) {
	defer h.c.downloads.InvalidateSnapshot()
	h.qbit.mu.Lock()
	defer h.qbit.mu.Unlock()
	for _, tr := range h.qbit.torrents {
		if tr["hash"] == hash {
			tr["state"], tr["content_path"] = state, contentPath
		}
	}
}

// deleted reports whether the fake client was asked to delete hash (with or without its
// data: removeIfNoVideo always asks for both).
func (h *lifecycleHarness) deleted(hash string) bool {
	for _, c := range h.qbit.callLog() {
		if c == "delete "+hash {
			return true
		}
	}
	return len(*h.removed) > 0
}

// pack writes the two-episode season pack into a fresh folder named waitPack.
func (h *lifecycleHarness) pack(t *testing.T) string {
	t.Helper()
	return h.download(t, waitPack, "Show.S01E01.1080p.WEB-DL-GRP.mkv", "Show.S01E02.1080p.WEB-DL-GRP.mkv")
}

// assertWaiting checks the download was left alone: nothing blocklisted or deleted, and its
// grab still grabbed, so the season isn't searched again.
func (h *lifecycleHarness) assertWaiting(t *testing.T, hash string, gid int64) {
	t.Helper()
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("%d release(s) blocklisted for a download that couldn't be read", n)
	}
	if got := h.status(t, gid); got != grabStatusGrabbed {
		t.Errorf("grab status = %q, want it still %q", got, grabStatusGrabbed)
	}
	if h.deleted(hash) {
		t.Errorf("the download was removed: %v %+v", h.qbit.callLog(), *h.removed)
	}
}

// A folder that can't be read is retried every sweep, never blocklisted, and shows in Needs
// you (one row, naming the folder) until it can be read; then it imports as normal.
func TestSeriesImportRetriesAFolderItCantRead(t *testing.T) {
	h := newLifecycleHarness(t)
	var logged syncBuffer
	h.c.log = slog.New(slog.NewTextHandler(&logged, nil))
	// Where qBittorrent says the files are: a folder that doesn't exist here, as an
	// incomplete folder doesn't inside a container that never mounted it.
	where := filepath.Join(t.TempDir(), "incomplete", waitPack)
	hash := h.completedTorrent(waitPack, seriesCategory, where)
	gid := h.seriesGrab(t, waitPack, hash)

	for range 3 {
		h.c.ImportSeriesDownloads(h.ctx)
	}
	h.assertWaiting(t, hash, gid)
	fails := h.c.SeriesImportFailures()
	if len(fails) != 1 || fails[0].Attempts != 3 || !strings.Contains(fails[0].LastErr, where) ||
		!strings.Contains(fails[0].LastErr, "can't read the download folder") {
		t.Fatalf("Needs you should list it once, naming the folder, after three sweeps: %+v", fails)
	}
	// Said once an hour, not every 30 seconds.
	if n := strings.Count(logged.String(), "can't read the download folder"); n != 1 {
		t.Errorf("logged %d times in three sweeps, want once", n)
	}
	h.later(61 * time.Minute)
	h.c.ImportSeriesDownloads(h.ctx)
	if n := strings.Count(logged.String(), "can't read the download folder"); n != 2 {
		t.Errorf("logged %d times after an hour, want twice", n)
	}

	// The folder appears (the share is back, the move is done): the next sweep imports it.
	if err := os.MkdirAll(filepath.Dir(where), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(h.pack(t), where); err != nil {
		t.Fatal(err)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if got := h.status(t, gid); got != grabStatusImported {
		t.Fatalf("after the folder appeared: grab status = %q, want %q", got, grabStatusImported)
	}
	if fails := h.c.SeriesImportFailures(); len(fails) != 0 {
		t.Errorf("Needs you still lists it after it imported: %+v", fails)
	}
	if n := h.blocklistCount(t); n != 0 {
		t.Errorf("%d release(s) blocklisted", n)
	}
}

// The other side of the same branch: a folder that was read and holds no video is junk —
// blocklisted, its grab failed and the download removed — exactly as before.
func TestSeriesImportStillBlocklistsAReadableFolderWithNoVideo(t *testing.T) {
	h := newLifecycleHarness(t)
	dir := filepath.Join(t.TempDir(), waitPack)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("nothing to see"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := h.completedTorrent(waitPack, seriesCategory, dir)
	gid := h.seriesGrab(t, waitPack, hash)

	h.c.ImportSeriesDownloads(h.ctx)

	blocked, err := h.c.blockedSetSeries(h.ctx, h.seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked[normTitle(waitPack)] {
		t.Error("a readable folder with no video in it wasn't blocklisted")
	}
	if got := h.status(t, gid); got != grabStatusFailed {
		t.Errorf("grab status = %q, want %q", got, grabStatusFailed)
	}
	if !h.deleted(hash) {
		t.Errorf("a junk download wasn't removed: %v", h.qbit.callLog())
	}
	if fails := h.c.SeriesImportFailures(); len(fails) != 0 {
		t.Errorf("a junk release isn't an import to retry: %+v", fails)
	}
}

// A torrent qBittorrent is still moving isn't offered to the import at all: nothing is
// read, noted, blocklisted or failed while the move runs. Once it seeds from its save
// folder, it imports from there.
func TestSeriesImportWaitsWhileQbittorrentMovesATorrent(t *testing.T) {
	h := newLifecycleHarness(t)
	incomplete := filepath.Join(t.TempDir(), "incomplete", waitPack)
	hash := h.completedTorrent(waitPack, seriesCategory, incomplete)
	gid := h.seriesGrab(t, waitPack, hash)
	h.setTorrent(hash, "moving", incomplete)

	for range 3 {
		h.c.ImportSeriesDownloads(h.ctx)
	}
	h.assertWaiting(t, hash, gid)
	if fails := h.c.SeriesImportFailures(); len(fails) != 0 {
		t.Errorf("a torrent mid-move isn't a failing import: %+v", fails)
	}

	h.setTorrent(hash, "stalledUP", h.pack(t))
	h.c.ImportSeriesDownloads(h.ctx)
	if got := h.status(t, gid); got != grabStatusImported {
		t.Fatalf("after the move: grab status = %q, want %q", got, grabStatusImported)
	}
}

// qBittorrent's missingFiles is the client saying the files aren't where it expects. The
// import waits (Needs you lists it as errored, from the client's own state); it isn't junk.
func TestSeriesImportLeavesMissingFilesAlone(t *testing.T) {
	h := newLifecycleHarness(t)
	gone := filepath.Join(t.TempDir(), waitPack)
	hash := h.completedTorrent(waitPack, seriesCategory, gone)
	gid := h.seriesGrab(t, waitPack, hash)
	h.setTorrent(hash, "missingFiles", gone)

	for range 2 {
		h.c.ImportSeriesDownloads(h.ctx)
	}
	h.assertWaiting(t, hash, gid)
}

// A folder inside the download that Arrmada isn't allowed into: the rest of the download
// is readable and holds no video, which used to read as junk — blocklisted, and its data
// deleted with the episodes still inside the folder it couldn't open. Now nothing is
// decided, and nothing is placed from a download only part of which could be read.
func TestSeriesImportNeverJudgesAPartlyUnreadableDownload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection doesn't work as root")
	}
	h := newLifecycleHarness(t)
	dir := filepath.Join(t.TempDir(), waitPack)
	sub := filepath.Join(dir, "Season 1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(h.pack(t), "Show.S01E01.1080p.WEB-DL-GRP.mkv"), filepath.Join(sub, "Show.S01E01.1080p.WEB-DL-GRP.mkv")); err != nil {
		t.Fatal(err)
	}
	unreadable(t, sub)
	hash := h.completedTorrent(waitPack, seriesCategory, dir)
	gid := h.seriesGrab(t, waitPack, hash)

	for range 2 {
		h.c.ImportSeriesDownloads(h.ctx)
	}
	h.assertWaiting(t, hash, gid)
	if fails := h.c.SeriesImportFailures(); len(fails) != 1 || !strings.Contains(fails[0].LastErr, "Season 1") {
		t.Errorf("Needs you should name the folder that couldn't be read: %+v", fails)
	}

	// The last check before deleting a download's data says "can't tell", and so the
	// download stays, whatever calls it.
	if found, err := hasAnyVideoFile(dir); err == nil || found {
		t.Errorf("hasAnyVideoFile = %v, %v; want an error", found, err)
	}
	h.c.removeIfNoVideo(h.ctx, hash, waitPack, dir)
	if h.deleted(hash) {
		t.Errorf("removeIfNoVideo deleted a download it couldn't read all of: %v", h.qbit.callLog())
	}

	// Importing it from Review says the folder can't be read, not "no video files".
	id := h.hold(t, hash, waitPack, dir)
	err := h.c.ImportReview(h.ctx, id, h.seriesID, "")
	if !errors.Is(err, ErrNothingToImport) || !strings.Contains(err.Error(), "can't read the download folder") {
		t.Errorf("Review import error = %v, want one saying the folder can't be read", err)
	}
}

// A season pack whose episodes are readable but which has a folder Arrmada can't open is
// held too: the unread folder may hold episodes, and a download imported in part is
// recorded as handled, so seed cleanup would later delete what was never looked at.
func TestSeriesImportHoldsAPackWithAnUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection doesn't work as root")
	}
	h := newLifecycleHarness(t)
	dir := h.pack(t)
	junk := filepath.Join(dir, "Extras")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	unreadable(t, junk)
	hash := h.completedTorrent(waitPack, seriesCategory, dir)
	gid := h.seriesGrab(t, waitPack, hash)

	h.c.ImportSeriesDownloads(h.ctx)
	h.assertWaiting(t, hash, gid)
	if evs := h.seriesEvents(t); hasEvent(evs, "imported", "") {
		t.Errorf("episodes were placed from a download only part of which was read: %+v", evs)
	}

	// Once the folder can be read, the pack imports.
	if err := os.Chmod(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	h.c.ImportSeriesDownloads(h.ctx)
	if got := h.status(t, gid); got != grabStatusImported {
		t.Fatalf("after the folder became readable: grab status = %q, want %q", got, grabStatusImported)
	}
}

// unreadable takes every permission off dir for the rest of the test.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}
