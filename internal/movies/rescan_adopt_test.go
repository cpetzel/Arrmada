package movies

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// What a rescan adopts when the recorded default is gone: the same file renamed by case
// only is still the film's; an extra, a hidden folder's file, a hardlink of a version's
// file or a loose film's neighbours never are.

// rescanOnce runs the Refresh rescan and returns the default it records afterwards.
func rescanOnce(t *testing.T, e *otherEnv, id int64) Movie {
	t.Helper()
	m, err := e.svc.repo.Get(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.rescan(e.ctx, &m)
	m, _ = e.svc.repo.Get(e.ctx, id)
	return m
}

func caseSensitive(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("a case-only rename keeps the recorded path here")
	}
}

// Renaming "Heat (1995).mkv" to "heat (1995).mkv" on Linux: the recorded file is gone, and
// the renamed one is the film's — not "recorded" because the two differ only in case.
func TestRescanAdoptsACaseOnlyRename(t *testing.T) {
	caseSensitive(t)
	for _, c := range []struct{ name, folder, from, to string }{
		{"name", "Heat (1995)", "Heat (1995).mkv", "heat (1995).mkv"},
		{"extension", "Heat (1995)", "Heat (1995).MKV", "Heat (1995).mkv"},
		{"folder not named the scheme's way", "Heat 1995 Bluray", "Heat (1995).mkv", "HEAT (1995).mkv"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newOtherEnv(t)
			rel := "Movies/" + c.folder + "/" + c.from
			old := e.file(t, rel, otherBig)
			m := e.film(t, 1, "Heat", 1995, rel)
			renamed := filepath.Join(filepath.Dir(old), c.to)
			if err := os.Rename(old, renamed); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 2; i++ { // and a second Refresh keeps it
				if m = rescanOnce(t, e, m.ID); !m.HasFile || m.MovieFilePath != renamed {
					t.Fatalf("rescan %d: default = %q (has_file %v), want %s", i, m.MovieFilePath, m.HasFile, c.to)
				}
			}
		})
	}
}

// A file differing from a record in the same folder only in case: on a case-insensitive
// share (CIFS with noserverino) it is that record's file renamed, so it's never adopted —
// the version keeps it, and the gone default's record is cleared. (The other-files list,
// which records nothing, still shows it on a case-sensitive disk.)
func TestRescanNeverAdoptsACaseVariantOfARecord(t *testing.T) {
	caseSensitive(t)
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) 1080p.mkv", otherBig)
	v := filepath.Join(e.base, "Movies/Heat (1995)/Heat (1995) 2160p.mkv")
	renamed := e.file(t, "Movies/Heat (1995)/heat (1995) 2160p.mkv", otherBig*2)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) 1080p.mkv")
	ver, _ := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "4K", QualityProfile: "uhd"})
	if err := e.svc.repo.SetVersionFile(e.ctx, ver.ID, v, otherBig*2, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.MovieFilePath == renamed || m.HasFile {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// The default gone, a version and extras left: neither the version's file nor a
// featurette or trailer becomes the default — the record is cleared.
func TestRescanNeverAdoptsAnExtra(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) 1080p.mkv", otherBig*2)
	v := e.file(t, "Movies/Heat (1995)/Heat (1995) 2160p.mkv", otherBig*4)
	e.file(t, "Movies/Heat (1995)/Featurettes/Making Of.mkv", otherBig*3)
	e.file(t, "Movies/Heat (1995)/Heat (1995)-trailer.mkv", otherBig*3)
	e.file(t, "Movies/Heat (1995)/Heat (1995) sample.mkv", otherBig*3)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) 1080p.mkv")
	ver, _ := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "4K", QualityProfile: "uhd"})
	if err := e.svc.repo.SetVersionFile(e.ctx, ver.ID, v, otherBig*4, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.HasFile || m.MovieFilePath != "" {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// A hardlink of a version's file under another name is that file: never the default.
func TestRescanNeverAdoptsAHardlinkOfAVersion(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995) 1080p.mkv", otherBig)
	v := e.file(t, "Movies/Heat (1995)/Heat (1995) 2160p.mkv", otherBig*2)
	if err := os.Link(v, filepath.Join(filepath.Dir(v), "Heat.1995.2160p.copy.mkv")); err != nil {
		t.Skip("no hardlinks here:", err)
	}
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) 1080p.mkv")
	ver, _ := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "4K", QualityProfile: "uhd"})
	if err := e.svc.repo.SetVersionFile(e.ctx, ver.ID, v, otherBig*2, ""); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.HasFile {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// A film loose in the Movies folder, its file gone: the Movies folder isn't its folder, so
// nothing there is adopted — not another film's, not the recycle bin's, not Convert's hold.
// The record is cleared, as when a film's own folder holds no video.
func TestRescanLooseFilmAdoptsNothing(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995).mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995).mkv")
	e.file(t, "Movies/Ronin (1998)/Ronin (1998).mkv", otherBig*3)
	e.film(t, 2, "Ronin", 1998, "Movies/Ronin (1998)/Ronin (1998).mkv")
	e.file(t, "Movies/Thief (1981).mkv", otherBig*2)
	e.file(t, "Movies/.arrmada-hold/Alien (1979)/Alien.1979.REMUX.mkv", otherBig*2)
	e.file(t, "Movies/.arrmada-recycle/Old/Old.mkv", otherBig*2)
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.HasFile || m.MovieFilePath != "" {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// Hidden folders inside a film's own folder are never adopted from either.
func TestRescanNeverAdoptsFromAHiddenFolder(t *testing.T) {
	e := newOtherEnv(t)
	def := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/.arrmada-hold/Heat.1995.REMUX.mkv", otherBig*2)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	_ = os.Remove(def)
	if m = rescanOnce(t, e, m.ID); m.HasFile {
		t.Fatalf("default = %q, want cleared", m.MovieFilePath)
	}
}

// What Review A confirmed stays: a hand upgrade under a new name is adopted, as is a new
// file beside a dangling link; the same name is kept; a bigger file beside a present
// default is not taken.
func TestRescanKeepsWhatWorked(t *testing.T) {
	t.Run("hand upgrade under a new name", func(t *testing.T) {
		e := newOtherEnv(t)
		d := e.file(t, "Movies/Heat (1995)/Heat (1995) 1080p.mkv", otherBig)
		m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) 1080p.mkv")
		_ = os.Remove(d)
		up := e.file(t, "Movies/Heat (1995)/Heat.1995.2160p.REMUX.mkv", otherBig*3)
		if m = rescanOnce(t, e, m.ID); m.MovieFilePath != up {
			t.Fatalf("default = %q, want the upgrade", m.MovieFilePath)
		}
	})
	t.Run("dangling link", func(t *testing.T) {
		e := newOtherEnv(t)
		p := filepath.Join(e.base, "Movies/Heat (1995)/Heat (1995).mkv")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.Symlink(filepath.Join(e.base, "Downloads/gone.mkv"), p); err != nil {
			t.Skip("no symlinks here:", err)
		}
		m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
		nf := e.file(t, "Movies/Heat (1995)/Heat.1995.new.mkv", otherBig)
		if m = rescanOnce(t, e, m.ID); m.MovieFilePath != nf {
			t.Fatalf("default = %q, want the new file", m.MovieFilePath)
		}
	})
	t.Run("same name, new contents", func(t *testing.T) {
		e := newOtherEnv(t)
		d := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
		m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
		e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig*2)
		if m = rescanOnce(t, e, m.ID); m.MovieFilePath != d {
			t.Fatalf("default = %q, want %q", m.MovieFilePath, d)
		}
	})
}

// A look through a library resolves each recorded path once; the next look within a short
// while (the page's list after a Refresh) reads the resolutions back instead of an Lstat per
// part of every path in the library. A newly recorded file is still seen at once.
func TestOtherFilesPassResolvesOncePerWhile(t *testing.T) {
	e := newOtherEnv(t)
	const n = 400
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("Movies/Film %d (2000)/Film %d (2000).mkv", i, i)
		e.film(t, 1000+i, fmt.Sprintf("Film %d", i), 2000, rel)
	}
	p1, err := e.svc.newOtherFilesPass(e.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	p1.index()
	if p1.resolved != n {
		t.Fatalf("first look resolved %d, want %d", p1.resolved, n)
	}
	p2, _ := e.svc.newOtherFilesPass(e.ctx, true)
	p2.index()
	if p2.resolved != 0 {
		t.Fatalf("second look resolved %d, want 0 (cached)", p2.resolved)
	}
	e.film(t, 9999, "New", 2001, "Movies/New (2001)/New (2001).mkv")
	p3, _ := e.svc.newOtherFilesPass(e.ctx, true)
	p3.index()
	if p3.resolved != 1 || !p3.recorded[fileKey(filepath.Join(e.base, "Movies/New (2001)/New (2001).mkv"))] {
		t.Fatalf("after a new record: resolved %d (want 1), recorded %v", p3.resolved, p3.recorded[fileKey(filepath.Join(e.base, "Movies/New (2001)/New (2001).mkv"))])
	}
}
