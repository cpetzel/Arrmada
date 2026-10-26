package movies

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The last round of what a rescan may adopt for a gone default: nothing when it can't see
// the whole folder, nothing in a shared folder, nothing recorded by a link retargeted a
// moment ago or a case-only rename of a record on a case-insensitive share — and a real
// replacement under a foreign-title release folder, or a film titled "… Other", is found.

// More entries than the walk reads: the rescan saw part of the folder, so it changes nothing.
func TestRescanTruncatedWalkChangesNothing(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	_ = os.Remove(def)
	for i := 0; i < otherFilesMaxEntries+100; i++ {
		e.file(t, fmt.Sprintf("Movies/Heat (1995)/%04d.jpg", i), 1)
	}
	e.file(t, "Movies/Heat (1995)/Heat.1995.2160p.mkv", otherBig*3)
	if m = rescanOnce(t, e, m.ID); !m.HasFile || m.MovieFilePath != def {
		t.Fatalf("default = %q (has_file %v), want the record unchanged", m.MovieFilePath, m.HasFile)
	}
}

// A film folder that can't be listed (a flaky mount, permissions): nothing changes.
func TestRescanUnreadableFolderChangesNothing(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	_ = os.Remove(def)
	e.file(t, "Movies/Heat (1995)/Heat.1995.2160p.mkv", otherBig*3)
	dir := filepath.Dir(def)
	readDir = func(d string) ([]os.DirEntry, error) {
		if d == dir {
			return nil, &os.PathError{Op: "open", Path: d, Err: os.ErrPermission}
		}
		return os.ReadDir(d)
	}
	t.Cleanup(func() { readDir = os.ReadDir })
	if m = rescanOnce(t, e, m.ID); !m.HasFile || m.MovieFilePath != def {
		t.Fatalf("default = %q (has_file %v), want the record unchanged", m.MovieFilePath, m.HasFile)
	}
}

// Another film records a link; its resolution is cached by the page's list; the link is
// then pointed at a file in this film's folder. Choosing a file to record resolves afresh:
// the file is that film's, and the folder shared, so nothing is adopted.
func TestRescanResolvesLinksAfresh(t *testing.T) {
	e := newOtherEnv(t)
	store := e.file(t, "Store/b.mkv", otherBig)
	link := filepath.Join(e.base, "Movies/Ronin (1998)/Ronin (1998).mkv")
	_ = os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(store, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	e.film(t, 2, "Ronin", 1998, "Movies/Ronin (1998)/Ronin (1998).mkv")
	def := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	x := e.file(t, "Movies/Heat (1995)/Ronin.1998.real.mkv", otherBig*3)
	if _, err := e.svc.OtherFilesLive(e.ctx, m.ID); err != nil { // fills the cache
		t.Fatal(err)
	}
	_ = os.Remove(link)
	if err := os.Symlink(x, link); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.MovieFilePath == x {
		t.Fatal("adopted the file another film records through a link")
	}
}

// A replacement in a release folder named with the film's foreign title and its year is
// the film's: adopted.
func TestRescanAdoptsFromAForeignTitleReleaseFolder(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Amelie (2001)/Amelie (2001).mkv", otherBig)
	m := e.film(t, 1, "Amelie", 2001, "Movies/Amelie (2001)/Amelie (2001).mkv")
	_ = os.Remove(def)
	repl := e.file(t, "Movies/Amelie (2001)/Le.Fabuleux.Destin.d.Amelie.Poulain.2001.1080p.BluRay-GRP/x.mkv", otherBig*3)
	if m = rescanOnce(t, e, m.ID); m.MovieFilePath != repl {
		t.Fatalf("default = %q, want the release folder's file", m.MovieFilePath)
	}
}

// A film titled "The Other": "The.Other.mkv" is the film, not an "-other" local extra —
// adopted, and listed as an other file when it isn't recorded.
func TestTitleEndingInAnExtraWord(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/The Other (1972)/The Other (1972).mkv", otherBig)
	m := e.film(t, 1, "The Other", 1972, "Movies/The Other (1972)/The Other (1972).mkv")
	copyf := e.file(t, "Movies/The Other (1972)/The.Other.mkv", otherBig*3)
	e.file(t, "Movies/The Other (1972)/The Other (1972)-other.mkv", otherBig*4) // a real extra
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil || fmt.Sprint(relNames(of)) != "[The.Other.mkv]" {
		t.Fatalf("other files = %v (%v), want only The.Other.mkv", relNames(of), err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.MovieFilePath != copyf {
		t.Fatalf("default = %q, want The.Other.mkv", m.MovieFilePath)
	}
}

// A folder that isn't provably the film's own adopts nothing: the record is cleared.
func TestRescanSharedFolderAdoptsNothing(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Collection/Heat (1995).mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Collection/Heat (1995).mkv")
	_ = os.Remove(def)
	e.file(t, "Movies/Collection/Heat.1995.1080p.mkv", otherBig)
	e.file(t, "Movies/Collection/Unmatched Film (2004).mkv", otherBig*3)
	if m = rescanOnce(t, e, m.ID); m.HasFile {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// A hardlink of the film's own version, the version stored in another folder: never the
// default.
func TestRescanNeverAdoptsAHardlinkOfOwnVersionElsewhere(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	v := e.file(t, "Movies/4K/Heat (1995) 2160p.mkv", otherBig*2)
	if err := os.Link(v, filepath.Join(filepath.Dir(def), "Heat.1995.2160p.copy.mkv")); err != nil {
		t.Skip("no hardlinks here:", err)
	}
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	ver, _ := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "4K", QualityProfile: "uhd"})
	if err := e.svc.repo.SetVersionFile(e.ctx, ver.ID, v, otherBig*2, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.HasFile {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}
