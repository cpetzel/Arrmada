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

// vanish makes the named files disappear between the walk listing them and looking at
// them, as a move, a recheck or a folder rename under the walk does.
func vanish(t *testing.T, names ...string) {
	t.Helper()
	gone := map[string]bool{}
	for _, n := range names {
		gone[n] = true
	}
	orig := entryInfo
	entryInfo = func(d fs.DirEntry) (fs.FileInfo, error) {
		if gone[d.Name()] {
			return nil, &fs.PathError{Op: "lstat", Path: d.Name(), Err: fs.ErrNotExist}
		}
		return d.Info()
	}
	t.Cleanup(func() { entryInfo = orig })
}

// The film vanishes mid-walk (moved out, rechecked back to ".!qB", a mover script) while
// its featurette stays: the import must not take the featurette for the film. The folder
// changed under it, so it's an error to retry (ErrChanged, a kind of ErrPartlyUnreadable).
func TestAVanishedFilmIsNeverReplacedByItsFeaturette(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Film.2020.1080p.mkv"), 90<<20)
	writeFile(t, filepath.Join(dir, "Film.2020.Featurette.mkv"), 60<<20)
	vanish(t, "Film.2020.1080p.mkv")

	got, _, err := findMediaFile(dir)
	if !errors.Is(err, ErrChanged) || !errors.Is(err, ErrPartlyUnreadable) || got != "" {
		t.Errorf("findMediaFile = %q, %v; want no pick and ErrChanged", got, err)
	}
	if _, err := FindVideos(dir); !errors.Is(err, ErrChanged) {
		t.Errorf("FindVideos err = %v, want ErrChanged", err)
	}
}

// A folder renamed as a whole after it was listed: every file is gone when it's looked at.
// That's a folder that changed, not one with no video in it.
func TestEveryFileVanishingIsAnErrorNotEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Show.S01E01.mkv"), 60<<20)
	writeFile(t, filepath.Join(dir, "Show.S01E02.mkv"), 60<<20)
	vanish(t, "Show.S01E01.mkv", "Show.S01E02.mkv")

	vids, err := FindVideos(dir)
	if !errors.Is(err, ErrChanged) || len(vids) != 0 {
		t.Errorf("FindVideos = %+v, %v; want nothing and ErrChanged", vids, err)
	}
	if _, _, err := FindVideo(dir); !errors.Is(err, ErrChanged) {
		t.Errorf("FindVideo err = %v, want ErrChanged", err)
	}
}

// A real read error outranks "changed": it's the one that won't clear by itself, so it's
// the one a person is told about.
func TestARealReadErrorOutranksAChange(t *testing.T) {
	got := firstUnreadable(firstUnreadable(nil, fs.ErrNotExist), fs.ErrPermission)
	if errors.Is(got, ErrChanged) || !errors.Is(got, fs.ErrPermission) {
		t.Errorf("err = %v, want the permission error", got)
	}
	if got := firstUnreadable(got, fs.ErrNotExist); !errors.Is(got, fs.ErrPermission) {
		t.Errorf("a later change replaced the read error: %v", got)
	}
}
