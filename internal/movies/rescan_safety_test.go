package movies

import (
	"os"
	"path/filepath"
	"testing"
)

// A rescan adopts a new default file only when the recorded one is gone from disk, and
// never a file a track of any film records. Before, it took the folder's biggest video
// whenever that differed from the record: an adopted 2160p copy (a version) became the
// default's file too, two tracks held one path, and "Remove version" recycled the file
// the default still recorded.

func TestRescanKeepsDefaultWhenAVersionIsBigger(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	big := e.file(t, "Movies/Heat (1995)/Heat (1995) Copy 2160p.mkv", otherBig+(20<<20))
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	v, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, big, "", "hd", "")
	if err != nil {
		t.Fatal(err)
	}
	// Refresh, as the page's button does (TMDB knows nothing here; the rescan still runs).
	e.svc.meta = fakeMeta{}
	if m, err = e.svc.Refresh(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if m.MovieFilePath != def {
		t.Fatalf("default now records %s, want %s", filepath.Base(m.MovieFilePath), filepath.Base(def))
	}
	// Remove version recycles the version's file; the default's stays.
	if err := e.svc.DeleteVersion(e.ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatalf("the default's file after Remove version: %v", err)
	}
}

// A version recorded the ordinary way (not adopted), bigger and in the same folder: the
// same — the default keeps its file.
func TestRescanKeepsDefaultBesideABiggerRecordedVersion(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	dc := e.file(t, "Movies/Heat (1995)/Heat (1995) {edition-Director's Cut} 2160p.mkv", otherBig+(30<<20))
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	v, err := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "Director's Cut", QualityProfile: "uhd", Monitored: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.repo.SetVersionFile(e.ctx, v.ID, dc, otherBig+(30<<20), ""); err != nil {
		t.Fatal(err)
	}
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	e.svc.rescan(e.ctx, &m)
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	if m.MovieFilePath != def || !m.HasFile {
		t.Fatalf("default now records %q (has_file %v), want %s", m.MovieFilePath, m.HasFile, filepath.Base(def))
	}
}

// The case the rule is for still works: the default's file is gone and another video is
// in the folder — it's adopted. But a version's file never is, even then.
func TestRescanReplacesAMissingDefaultButNotWithAVersionsFile(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	dc := e.file(t, "Movies/Heat (1995)/Heat (1995) Directors Cut.mkv", otherBig+(30<<20))
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	v, _ := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "Director's Cut", QualityProfile: "uhd"})
	if err := e.svc.repo.SetVersionFile(e.ctx, v.ID, dc, otherBig+(30<<20), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	// Only the version's file is there: nothing to adopt, so the record is cleared.
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	if e.svc.rescan(e.ctx, &m) {
		t.Fatal("the rescan adopted the version's file as the default")
	}
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	if m.HasFile || m.MovieFilePath != "" {
		t.Fatalf("default = %q (has_file %v), want cleared", m.MovieFilePath, m.HasFile)
	}
	// A replacement put there by hand is adopted.
	repl := e.file(t, "Movies/Heat (1995)/Heat.1995.1080p.WEB-DL.mkv", otherBig)
	if !e.svc.rescan(e.ctx, &m) {
		t.Fatal("the rescan didn't adopt the replacement")
	}
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	if m.MovieFilePath != repl {
		t.Fatalf("default = %q, want the replacement", m.MovieFilePath)
	}
}

// Another film's recorded file in a shared folder is never adopted as this film's.
func TestRescanNeverAdoptsAnotherFilmsFile(t *testing.T) {
	e := newOtherEnv(t)
	heatPath := e.file(t, "Movies/Shared/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Shared/Ronin (1998).mkv", otherBig+(10<<20))
	heat := e.film(t, 1, "Heat", 1995, "Movies/Shared/Heat (1995).mkv")
	ronin := e.film(t, 2, "Ronin", 1998, "Movies/Shared/Ronin (1998).mkv")
	if err := os.Remove(heatPath); err != nil {
		t.Fatal(err)
	}
	if e.svc.rescan(e.ctx, &heat) {
		t.Fatal("Heat adopted Ronin's file")
	}
	heat, _ = e.svc.repo.Get(e.ctx, heat.ID)
	if heat.HasFile {
		t.Fatalf("Heat = %q, want cleared", heat.MovieFilePath)
	}
	if r, _ := e.svc.repo.Get(e.ctx, ronin.ID); !r.HasFile {
		t.Fatal("Ronin lost its file")
	}
}

// A recorded default that's on disk but small (under the 50 MB sample floor) is kept: the
// rescan used to find "no video" in its folder and clear the record.
func TestRescanKeepsASmallRecordedDefault(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Short (2001)/Short (2001).mkv", 20<<20)
	m := e.film(t, 1, "Short", 2001, "Movies/Short (2001)/Short (2001).mkv")
	e.svc.rescan(e.ctx, &m)
	m, _ = e.svc.repo.Get(e.ctx, m.ID)
	if !m.HasFile || m.MovieFilePath != def {
		t.Fatalf("default = %q (has_file %v), want the small file kept", m.MovieFilePath, m.HasFile)
	}
}
