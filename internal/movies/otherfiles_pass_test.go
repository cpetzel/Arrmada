package movies

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A film's folder holding another film's folder ("Alien (1979)/Aliens (1986)/…"): that
// folder's videos are the other film's, never this one's other files. A subfolder named
// for this film, or for nothing in particular, is still looked in.
func TestOtherFilesSkipAnotherFilmsFolderInside(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Alien (1979)/Alien (1979).mkv", otherBig)
	e.file(t, "Movies/Alien (1979)/Aliens (1986)/Aliens.mkv", otherBig)
	e.file(t, "Movies/Alien (1979)/Alien 3 (1992)/Alien3.mkv", otherBig)
	e.file(t, "Movies/Alien (1979)/Alien (1979) Directors Cut/Alien DC.mkv", otherBig)
	e.file(t, "Movies/Alien (1979)/Cuts/Alien Theatrical.mkv", otherBig)
	m := e.film(t, 1, "Alien", 1979, "Movies/Alien (1979)/Alien (1979).mkv")
	of, err := e.svc.OtherFilesLive(e.ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := relNames(of)
	want := []string{"Alien (1979) Directors Cut/Alien DC.mkv", "Cuts/Alien Theatrical.mkv"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("other files = %v, want %v", got, want)
	}
}

// Looking again at an unchanged folder writes nothing (checked_at keeps the last change);
// a change is written.
func TestOtherFilesUnchangedLookWritesNothing(t *testing.T) {
	e := newOtherEnv(t)
	e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
	e.file(t, "Movies/Heat (1995)/Heat (1995) Copy.mkv", otherBig)
	m := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
	if _, err := e.svc.OtherFilesLive(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.repo.db.Exec(`UPDATE movie_other_files SET checked_at = 'before' WHERE movie_id = ?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OtherFilesLive(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := e.svc.repo.otherFiles(e.ctx, m.ID); st.CheckedAt != "before" {
		t.Fatalf("an unchanged look wrote the row (checked_at %q)", st.CheckedAt)
	}
	e.file(t, "Movies/Heat (1995)/Heat (1995) Another.mkv", otherBig)
	if _, err := e.svc.OtherFilesLive(e.ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := e.svc.repo.otherFiles(e.ctx, m.ID); st.CheckedAt == "before" || st.Count != 2 {
		t.Fatalf("a changed folder wasn't written: %+v", st)
	}
}

// passOf is a pass over records (no database): what a scan builds from RecordedPaths.
func passOf(paths []string) *otherFilesPass {
	p := &otherFilesPass{recorded: map[string]bool{}, under: map[string][]int32{}, probe: newLinkProbe(nil)}
	for _, x := range paths {
		p.note(x)
	}
	return p
}

// A scan's folder questions cost a lookup each, not a pass over every record: with N films
// in their own folders, asking each folder examines about one record (its own), where the
// old loop examined N per folder (N² in all).
func TestOtherFilesPassLookupIsBounded(t *testing.T) {
	root := t.TempDir()
	const n = 3000
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(root, "Movies", fmt.Sprintf("Film %d (2000)", i), "film.mkv")
	}
	p := passOf(paths)
	for i, x := range paths {
		if o := p.recordedIntoExcept(filepath.Dir(x), map[string]bool{pathKey(x): true}); o != "" {
			t.Fatalf("film %d: %q reported in its own folder", i, o)
		}
	}
	if p.looked > 3*n {
		t.Fatalf("examined %d records for %d folders, want about one each", p.looked, n)
	}
	// And the answer is still right: a record of another film in a folder is found.
	if o := p.recordedIntoExcept(filepath.Join(root, "Movies"), map[string]bool{}); o == "" {
		t.Fatal("the Movies folder holds records, none reported")
	}
}

func BenchmarkOtherFilesPassLookup(b *testing.B) {
	root := b.TempDir()
	const n = 10000
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(root, "Movies", fmt.Sprintf("Film %d (2000)", i), "film.mkv")
	}
	p := passOf(paths)
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		x := paths[k%n]
		p.recordedIntoExcept(filepath.Dir(x), map[string]bool{pathKey(x): true})
	}
}

// A file adopted as a version in a folder two films share, then a delete with files: the
// deleted film's files (the adopted one included) go to the bin, the other film's stay —
// whichever film is deleted.
func TestAdoptInSharedFolderThenDeleteWithFiles(t *testing.T) {
	for _, gone := range []string{"Heat", "Ronin"} {
		t.Run("delete "+gone, func(t *testing.T) {
			e := newOtherEnv(t)
			heatFile := e.file(t, "Movies/Heat (1995)/Heat (1995).mkv", otherBig)
			roninFile := e.file(t, "Movies/Heat (1995)/Ronin (1998).mkv", otherBig)
			extra := e.file(t, "Movies/Heat (1995)/Collateral (2004).mkv", otherBig)
			heat := e.film(t, 1, "Heat", 1995, "Movies/Heat (1995)/Heat (1995).mkv")
			ronin := e.film(t, 2, "Ronin", 1998, "Movies/Heat (1995)/Ronin (1998).mkv")
			if _, err := e.svc.AddOtherFileAsVersion(e.ctx, heat.ID, extra, "Kept copy", "hd", ""); err != nil {
				t.Fatal(err)
			}
			id, keep, goneFiles := heat.ID, []string{roninFile}, []string{heatFile, extra}
			if gone == "Ronin" {
				id, keep, goneFiles = ronin.ID, []string{heatFile, extra}, []string{roninFile}
			}
			if err := e.svc.Delete(e.ctx, id, true); err != nil {
				t.Fatal(err)
			}
			for _, p := range keep {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("%s must survive: %v", filepath.Base(p), err)
				}
			}
			for _, p := range goneFiles {
				if _, err := os.Stat(p); !os.IsNotExist(err) {
					t.Errorf("%s should have gone to the bin (err %v)", filepath.Base(p), err)
				}
			}
		})
	}
}
