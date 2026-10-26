package movies

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/eventbus"
	"github.com/tristenlammi/arrmada/internal/mediainfo"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The other files in a film's folder: the videos Arrmada doesn't use, never the film's own,
// another film's, samples or local extras — and, in a folder that isn't provably the
// film's own, only what no film records, at the folder's top level.

func TestOtherFileSkip(t *testing.T) {
	for _, c := range []struct {
		rel  string
		size int64
		want bool // counts as an other file
	}{
		{"Heat (1995) Extended.mkv", otherBig, true},
		{"Heat.1995.2160p.UHD.mkv", otherBig, true},
		{"Cuts/Heat (1995) Directors Cut.mkv", otherBig, true},
		{"Heat (1995).nfo", otherBig, false},
		{"Heat (1995).en.srt", otherBig, false},
		{"poster.jpg", otherBig, false},
		// samples: by name, by folder, by size
		{"heat-sample.mkv", otherBig, false},
		{"Sample/heat.mkv", otherBig, false},
		{"samples/heat.mkv", otherBig, false},
		{"Heat (1995) small.mkv", 10 << 20, false},
		{"The Sampler (2004).mkv", otherBig, true}, // "Sampler" is a title, not a sample
		// Plex/Jellyfin local-extras names
		{"Heat (1995)-trailer.mkv", otherBig, false},
		{"Heat (1995)-featurette.mp4", otherBig, false},
		{"Heat (1995)-behindthescenes.mkv", otherBig, false},
		{"Heat (1995)-deleted.mkv", otherBig, false},
		{"Heat (1995)-interview.mkv", otherBig, false},
		{"Heat (1995)-scene.mkv", otherBig, false},
		{"Heat (1995)-short.mkv", otherBig, false},
		{"Heat (1995)-other.mkv", otherBig, false},
		{"Heat (1995).trailer.mkv", otherBig, false},
		{"Heat (1995)_Trailer.mkv", otherBig, false},
		{"Heat (1995) Trailer Park.mkv", otherBig, true}, // not an ending
		// extras folders, at any depth below the film's folder
		{"Extras/Making Of.mkv", otherBig, false},
		{"Featurettes/x.mkv", otherBig, false},
		{"Trailers/x.mkv", otherBig, false},
		{"Behind The Scenes/x.mkv", otherBig, false},
		{"Deleted Scenes/x.mkv", otherBig, false},
		{"Interviews/x.mkv", otherBig, false},
		{"Scenes/x.mkv", otherBig, false},
		{"Shorts/x.mkv", otherBig, false},
		{"Other/x.mkv", otherBig, false},
		{"Specials/x.mkv", otherBig, false},
		{"Bonus/Disc 2/x.mkv", otherBig, false},
		{"Cuts/extras/x.mkv", otherBig, false},
	} {
		rel := filepath.FromSlash(c.rel)
		if got := otherFileSkip(rel, c.size) == ""; got != c.want {
			t.Errorf("%s: counts = %v, want %v (%q)", c.rel, got, c.want, otherFileSkip(rel, c.size))
		}
	}
}

type otherEnv struct {
	svc  *Service
	ctx  context.Context
	base string // holds Movies (the Movies folder) and Downloads
}

func newOtherEnv(t *testing.T) *otherEnv {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	base := t.TempDir()
	root := filepath.Join(base, "Movies")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(st.DB(), nil, nil, root, filepath.Join(base, "bin"), eventbus.New(log), log)
	svc.SetProtectedDirs(func() []string { return []string{filepath.Join(base, "Downloads")} })
	// No ffprobe: the name and the size are all a file says.
	svc.SetFileAccess(func(string) (mediainfo.Info, error) { return mediainfo.Info{}, errors.New("no probe") }, os.Stat)
	return &otherEnv{svc: svc, ctx: context.Background(), base: base}
}

// file writes a sparse file of size bytes at rel (under base) and returns its path.
func (e *otherEnv) file(t *testing.T, rel string, size int64) string {
	t.Helper()
	p := filepath.Join(e.base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

func (e *otherEnv) film(t *testing.T, tmdb int, title string, year int, rel string) Movie {
	t.Helper()
	m, err := e.svc.repo.Create(e.ctx, Movie{TMDBID: tmdb, Title: title, Year: year})
	if err != nil {
		t.Fatal(err)
	}
	if rel != "" {
		p := filepath.Join(e.base, filepath.FromSlash(rel))
		if err := e.svc.repo.SetFile(e.ctx, m.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	m, err = e.svc.repo.Get(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func relNames(of OtherFiles) []string {
	out := []string{}
	for _, f := range of.Files {
		out = append(out, f.RelPath)
	}
	sort.Strings(out)
	return out
}

const otherBig = 60 << 20 // over the 50 MB sample floor

func TestOtherFilesOwnFolder(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Copy 2160p.mkv", otherBig)           // counts
	e.file(t, "Movies/Heat (1995)/Heat (1995) Directors Cut.mkv", otherBig)        // a version's
	e.file(t, "Movies/Heat (1995)/Cuts/Heat (1995) Theatrical.mkv", otherBig)      // counts: one down
	e.file(t, "Movies/Heat (1995)/Cuts/Old/Heat (1995) Old Copy.mkv", otherBig)    // counts: two down
	e.file(t, "Movies/Heat (1995)/Cuts/Old/Deeper/Heat (1995) Deep.mkv", otherBig) // three down: not looked at
	e.file(t, "Movies/Heat (1995)/Heat (1995)-trailer.mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Featurettes/Making Heat.mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Sample/heat.mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/heat-sample.mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Heat (1995) tiny.mkv", 1<<20)
	e.file(t, "Movies/Heat (1995)/Heat (1995).nfo", 10)
	e.file(t, "Movies/Heat (1995)/.arrmada-recycle/Heat (1995) old.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	v, err := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "Director's Cut", QualityProfile: "hd"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.repo.SetVersionFile(e.ctx, v.ID, filepath.Join(e.base, "Movies/Heat (1995)/Heat (1995) Directors Cut.mkv"), otherBig, ""); err != nil {
		t.Fatal(err)
	}
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Cuts/Heat (1995) Theatrical.mkv", "Cuts/Old/Heat (1995) Old Copy.mkv", "Heat (1995) Copy 2160p.mkv"}
	if got := relNames(of); strings.Join(got, "|") != strings.Join(want, "|") || of.Count != 3 || of.Shared {
		t.Fatalf("other files = %v (count %d, shared %v), want %v", got, of.Count, of.Shared, want)
	}
	for _, f := range of.Files {
		if f.Name == "Heat (1995) Copy 2160p.mkv" && f.Resolution != "2160p" {
			t.Errorf("resolution from the name = %q, want 2160p", f.Resolution)
		}
	}
	// Stored: the list's count and the page's chip, without touching the disk.
	sums, err := e.svc.ListSummaries(e.ctx)
	if err != nil || len(sums) != 1 || sums[0].OtherFiles != 3 {
		t.Fatalf("summary other files = %+v (%v), want 3", sums, err)
	}
	if st := e.svc.StoredOtherFiles(e.ctx, m); st == nil || st.Count != 3 || len(st.Files) != 3 {
		t.Fatalf("stored = %+v, want 3 files", st)
	}
	// Deleting the film takes its row with it.
	if err := e.svc.repo.Delete(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.svc.repo.otherFiles(e.ctx, m.ID); ok {
		t.Error("the other-files row outlived the film")
	}
}

// A hardlink of the film's own file under another name is the same file, not another one.
func TestOtherFilesHardlinkOfOwnFile(t *testing.T) {
	e := newOtherEnv(t)
	main := e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	if err := os.Link(main, filepath.Join(filepath.Dir(main), "Heat.1995.1080p.BluRay.x264-GRP.mkv")); err != nil {
		t.Skip("no hardlinks here:", err)
	}
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil || of.Count != 0 {
		t.Fatalf("other files = %v (%v), want none", relNames(of), err)
	}
}

// Two films recorded in one folder: neither is the other's other file. The folder isn't
// either's own, so only files no film records count, and only at its top level.
func TestOtherFilesSharedFolder(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Ronin (1998).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Collateral (2004).mkv", otherBig) // no film records it
	e.file(t, "Movies/Heat (1995)/More/Thief (1981).mkv", otherBig) // a subfolder: not looked in
	heat := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	ronin := e.film(t, 2, "Ronin", 1998, "Movies/Heat (1995)/Ronin (1998).mkv")
	for _, m := range []Movie{heat, ronin} {
		of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := relNames(of); len(got) != 1 || got[0] != "Collateral (2004).mkv" || !of.Shared {
			t.Errorf("%s: other files = %v (shared %v), want only Collateral, shared", m.Title, got, of.Shared)
		}
	}
}

// Another film's record pointing into a subfolder makes the folder shared too, even with
// the file gone from disk.
func TestOtherFilesAnotherRecordBelow(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Cuts/Heat (1995) Extended.mkv", otherBig)
	heat := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	e.film(t, 2, "Ronin", 1998, "Movies/Heat (1995)/Ronin/Ronin (1998).mkv") // missing from disk
	of, err := e.svc.OtherFilesLive(e.ctx, heat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !of.Shared || of.Count != 0 {
		t.Fatalf("other files = %v (shared %v), want none, shared", relNames(of), of.Shared)
	}
}

// A folder not named for the film (a franchise folder) isn't provably its own: top level
// only, never the films in its subfolders.
func TestOtherFilesFolderNotNamedForTheFilm(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/LOTR/The Fellowship of the Ring (2001).mkv", otherBig)
	e.file(t, "Movies/LOTR/The Fellowship of the Ring (2001) Extended.mkv", otherBig)
	e.file(t, "Movies/LOTR/The Two Towers (2002)/The Two Towers (2002).mkv", otherBig)
	m := e.film(t, 120, "The Lord of the Rings: The Fellowship of the Ring", 2001, "Movies/LOTR/The Fellowship of the Ring (2001).mkv")
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := relNames(of); len(got) != 1 || got[0] != "The Fellowship of the Ring (2001) Extended.mkv" || !of.Shared {
		t.Fatalf("other files = %v (shared %v), want the Extended file only, shared", got, of.Shared)
	}
}

// A film loose in the Movies folder, or in a downloads folder, has no folder of its own to
// look in: nothing is reported (every other loose film would be).
func TestOtherFilesLooseInALibraryFolder(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Ronin (1998).mkv", otherBig)
	e.file(t, "Downloads/Thief (1981).mkv", otherBig)
	e.file(t, "Downloads/Collateral (2004).mkv", otherBig)
	heat := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995).mkv")
	thief := e.film(t, 2, "Thief", 1981, "Downloads/Thief (1981).mkv")
	for _, m := range []Movie{heat, thief} {
		of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
		if err != nil || of.Count != 0 {
			t.Errorf("%s: other files = %v (%v), want none", m.Title, relNames(of), err)
		}
		if e.svc.StoredOtherFiles(e.ctx, m) != nil {
			t.Errorf("%s: a row was stored for a folder that isn't looked at", m.Title)
		}
	}
}

// Add as a version records the file where it is: nothing on disk moves, and the file is
// no longer an other file.
func TestAddOtherFileAsVersionRecordsInPlace(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	extra := e.file(t, "Movies/Heat (1995)/Heat (1995) Copy 2160p.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	before := listTree(t, filepath.Join(e.base, "Movies"))

	if _, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, filepath.Join(e.base, "Movies/Heat (1995)/nope.mkv"), "", "hd", ""); !errors.Is(err, ErrNotOtherFile) {
		t.Fatalf("a path that isn't an other file: err = %v, want ErrNotOtherFile", err)
	}
	v, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, extra, "", "hd", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.FilePath != extra || !v.HasFile || v.Monitored || v.Label != "2160p" {
		t.Fatalf("version = %+v, want the file where it is, unmonitored, labelled 2160p", v)
	}
	if taken, err := e.svc.repo.pathRecorded(e.ctx, extra); err != nil || !taken {
		t.Fatalf("pathRecorded = %v (%v), want true: a second add must find it taken", taken, err)
	}
	if after := listTree(t, filepath.Join(e.base, "Movies")); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Fatalf("the folder changed:\nbefore %v\nafter  %v", before, after)
	}
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil || of.Count != 0 {
		t.Fatalf("other files after = %v (%v), want none", relNames(of), err)
	}
	// Again: it's the version's file now, not an other file.
	if _, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, extra, "", "hd", ""); !errors.Is(err, ErrNotOtherFile) {
		t.Fatalf("again: err = %v, want ErrNotOtherFile", err)
	}
	if vs, _ := e.svc.repo.ListVersions(e.ctx, m.ID); len(vs) != 1 {
		t.Fatalf("versions = %d, want 1 (a refused add leaves none behind)", len(vs))
	}
}

// A file gone between the list and the click leaves no version behind.
func TestAddOtherFileGoneBeforeRecording(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	extra := e.file(t, "Movies/Heat (1995)/Heat (1995) Extended.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	// The walk finds it; the read just before recording finds it gone.
	e.svc.SetFileAccess(func(string) (mediainfo.Info, error) { return mediainfo.Info{}, errors.New("no probe") },
		func(p string) (os.FileInfo, error) {
			if p == extra {
				return nil, os.ErrNotExist
			}
			return os.Stat(p)
		})
	if _, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, extra, "", "hd", ""); !errors.Is(err, ErrNotOtherFile) {
		t.Fatalf("err = %v, want ErrNotOtherFile", err)
	}
	if vs, _ := e.svc.repo.ListVersions(e.ctx, m.ID); len(vs) != 0 {
		t.Fatalf("versions = %+v, want none left behind", vs)
	}
}

// Another track recording the file between the list and the transaction (a second click,
// another tab): the transaction refuses, and the version made for it goes again.
func TestAddOtherFileRecordedMeanwhileRollsBack(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	extra := e.file(t, "Movies/Heat (1995)/Heat (1995) Extended.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	other, err := e.svc.repo.CreateVersion(e.ctx, m.ID, Version{Label: "Other tab", QualityProfile: "hd"})
	if err != nil {
		t.Fatal(err)
	}
	// The read just before recording is where the other tab's click lands.
	e.svc.SetFileAccess(func(string) (mediainfo.Info, error) { return mediainfo.Info{}, errors.New("no probe") },
		func(p string) (os.FileInfo, error) {
			if p == extra {
				if err := e.svc.repo.SetVersionFile(e.ctx, other.ID, extra, otherBig, ""); err != nil {
					t.Error(err)
				}
			}
			return os.Stat(p)
		})
	if _, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, extra, "Extended", "hd", ""); !errors.Is(err, ErrNotOtherFile) {
		t.Fatalf("err = %v, want ErrNotOtherFile", err)
	}
	vs, _ := e.svc.repo.ListVersions(e.ctx, m.ID)
	if len(vs) != 1 || vs[0].ID != other.ID {
		t.Fatalf("versions = %+v, want only the other tab's", vs)
	}
}

func listTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// A library scan notes the other files in each film's folder as it reaches it — a film it
// adds, and one it already knew (skipped) — so the movies list has the count.
func TestScanNotesOtherFiles(t *testing.T) {
	svc, _, root := scanFixture(t, "Movie (2010)/Movie.2010.1080p.mkv", "Other (2020)/Other.2020.720p.mkv")
	videoFixture(t, filepath.Join(root, "Movie (2010)", "Movie.2010.Open.Matte.mkv"))
	videoFixture(t, filepath.Join(root, "Movie (2010)", "Movie (2010)-trailer.mkv"))
	// The film's own file is the folder's biggest, which is what the scan takes.
	if err := os.Truncate(filepath.Join(root, "Movie (2010)", "Movie.2010.1080p.mkv"), 60<<20); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := svc.ScanLibrary(ctx, "", ScanOptions{}); err != nil {
			t.Fatal(err)
		}
		sums, err := svc.ListSummaries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, s := range sums {
			got[s.Title] = s.OtherFiles
		}
		if len(got) != 2 || got["Movie"] != 1 || got["Other"] != 0 {
			t.Fatalf("scan %d: other files = %v, want Movie 1, Other 0", i+1, got)
		}
	}
}

// A record made through a link to the film's folder still points into it: the folder is
// shared, and the other film's file is never listed as this one's.
func TestOtherFilesRecordThroughALink(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Ronin (1998).mkv", otherBig)
	if err := os.Symlink(filepath.Join(e.base, "Movies", "Heat (1995)"), filepath.Join(e.base, "Movies", "Alias")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	heat := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	e.film(t, 2, "Ronin", 1998, "Movies/Alias/Ronin (1998).mkv")
	of, err := e.svc.OtherFilesLive(e.ctx, heat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !of.Shared || of.Count != 0 {
		t.Fatalf("other files = %v (shared %v), want none, shared: Ronin's file is Ronin's", relNames(of), of.Shared)
	}
}

// Stop tracking (keep the file): the version's record goes, its file stays exactly where it
// was, and it's one of the film's other files again. Remove version, by contrast, recycles it.
func TestUntrackVersionKeepsTheFile(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv", otherBig)
	extra := e.file(t, "Movies/Heat (1995)/Heat (1995) Copy 2160p.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995) Bluray-1080p.mkv")
	v, err := e.svc.AddOtherFileAsVersion(e.ctx, m.ID, extra, "", "hd", "")
	if err != nil {
		t.Fatal(err)
	}
	before := listTree(t, filepath.Join(e.base, "Movies"))
	other, _ := e.svc.repo.Create(e.ctx, Movie{TMDBID: 9, Title: "Ronin", Year: 1998})
	if err := e.svc.UntrackVersion(e.ctx, other.ID, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("through another film: err = %v, want ErrNotFound", err)
	}
	if err := e.svc.UntrackVersion(e.ctx, m.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("the file after Stop tracking: %v", err)
	}
	if after := listTree(t, filepath.Join(e.base, "Movies")); strings.Join(after, "|") != strings.Join(before, "|") {
		t.Fatalf("the folder changed:\nbefore %v\nafter  %v", before, after)
	}
	if vs, _ := e.svc.repo.ListVersions(e.ctx, m.ID); len(vs) != 0 {
		t.Fatalf("versions = %+v, want none", vs)
	}
	if st := e.svc.StoredOtherFiles(e.ctx, m); st == nil || st.Count != 1 {
		t.Fatalf("stored other files = %+v, want the file back", st)
	}
}
