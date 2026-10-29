package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A download with a folder inside it Arrmada can't open: the walks used to skip that folder
// in silence, so episodes inside it didn't exist as far as anything could tell, and a
// download whose videos were all in there read as one with no video at all.
//
// Now every walk that decides something says it couldn't read all of it. A listing still
// gets what was found; an import refuses (it can't know the largest video, or every
// episode, without the rest); FindVideo, which finds film folders already in the library,
// settles for what it could read.
func TestWalksReportAFolderTheyCantRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection doesn't work as root")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Film.2020.1080p.mkv"), 60<<20)
	locked := filepath.Join(dir, "Featurettes")
	writeFile(t, filepath.Join(locked, "Film.2020.Making.Of.mkv"), 90<<20)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	vids, truncated, err := FindVideosCtx(context.Background(), dir, 0, 0)
	if !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("FindVideosCtx err = %v, want ErrPartlyUnreadable", err)
	}
	if truncated || len(vids) != 1 || filepath.Base(vids[0].Path) != "Film.2020.1080p.mkv" {
		t.Errorf("FindVideosCtx = %+v (truncated %v), want the one video it could read", vids, truncated)
	}
	if _, err := FindVideos(dir); !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("FindVideos err = %v, want ErrPartlyUnreadable", err)
	}

	if got, _, err := findMediaFile(dir); !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("findMediaFile = %q, %v; an import must not pick from part of a download", got, err)
	}
	if got, _, err := FindVideo(dir); err != nil || filepath.Base(got) != "Film.2020.1080p.mkv" {
		t.Errorf("FindVideo = %q, %v; want the film it could read", got, err)
	}

	// With nothing readable but the locked folder, it's still an error, never "no video".
	only := t.TempDir()
	inner := filepath.Join(only, "Show.S01")
	writeFile(t, filepath.Join(inner, "Show.S01E01.mkv"), 60<<20)
	if err := os.Chmod(inner, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(inner, 0o755) })
	if vids, err := FindVideos(only); !errors.Is(err, ErrPartlyUnreadable) || len(vids) != 0 {
		t.Errorf("FindVideos = %+v, %v; want nothing found and ErrPartlyUnreadable", vids, err)
	}
	if _, _, err := FindVideo(only); !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("FindVideo err = %v, want ErrPartlyUnreadable", err)
	}
}

// A readable folder reads as it always did: no error, empty or not.
func TestWalksOfAReadableFolderHaveNoError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "readme.txt"), 10)
	if vids, err := FindVideos(dir); err != nil || len(vids) != 0 {
		t.Errorf("FindVideos = %+v, %v; want nothing, no error", vids, err)
	}
	if _, err := FindVideos(filepath.Join(dir, "missing")); err == nil || errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("a missing folder: err = %v, want the stat error", err)
	}
}

// A file listed and gone by the time it's looked at (qBittorrent renaming "x.mkv.!qB" as a
// torrent completes, a sample deleted by hand) is skipped: it isn't a part of the download
// Arrmada can't read, and holding the download for it would hold it for nothing. A file
// that's there but can't be looked at (an I/O error, permissions) still is one.
func TestWalksSkipAFileThatVanished(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Show.S01E01.mkv"), 60<<20)
	writeFile(t, filepath.Join(dir, "Show.S01E02.mkv"), 60<<20)
	fail := fs.ErrNotExist
	orig := entryInfo
	entryInfo = func(d fs.DirEntry) (fs.FileInfo, error) {
		if d.Name() == "Show.S01E02.mkv" {
			return nil, &fs.PathError{Op: "lstat", Path: d.Name(), Err: fail}
		}
		return d.Info()
	}
	t.Cleanup(func() { entryInfo = orig })

	vids, err := FindVideos(dir)
	if err != nil || len(vids) != 1 || filepath.Base(vids[0].Path) != "Show.S01E01.mkv" {
		t.Errorf("a vanished file: FindVideos = %+v, %v; want the other episode and no error", vids, err)
	}
	if got, _, err := findMediaFile(dir); err != nil || filepath.Base(got) != "Show.S01E01.mkv" {
		t.Errorf("a vanished file: findMediaFile = %q, %v", got, err)
	}

	fail = fs.ErrPermission
	if _, err := FindVideos(dir); !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("a file that can't be stat'd: err = %v, want ErrPartlyUnreadable", err)
	}
	if _, _, err := findMediaFile(dir); !errors.Is(err, ErrPartlyUnreadable) {
		t.Errorf("a file that can't be stat'd: findMediaFile err = %v, want ErrPartlyUnreadable", err)
	}
}
