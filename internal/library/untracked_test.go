package library

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// An import whose scheme name is already taken by a file no record owns — a remux kept by
// hand under "Heat (1995) - 1080p BluRay.mkv" — keeps that file and lands beside it. It
// used to go to the recycle bin (or, with the bin off, be overwritten). All on synthetic
// files in temp dirs.

const heatRelease = "Heat.1995.1080p.BluRay.x264-GRP"

func heatRig(t *testing.T, binOn bool) (im *Importer, lib, bin, dl, theirs string) {
	t.Helper()
	src, lib := t.TempDir(), t.TempDir()
	im = NewImporter(lib, quiet())
	if binOn {
		bin = t.TempDir()
		im.SetRecycleDir(bin)
	}
	theirs = filepath.Join(lib, "Heat (1995)", "Heat (1995) - 1080p BluRay.mkv")
	writeBytes(t, theirs, patterned(4096, 'u', 10, "THEIRS"))
	dl = filepath.Join(src, heatRelease+".mkv")
	writeBytes(t, dl, patterned(4096, 'n', 10, "NEW"))
	return im, lib, bin, dl, theirs
}

func TestImportAsNamedKeepsUntrackedFile(t *testing.T) {
	for _, binOn := range []bool{true, false} {
		name := "bin off"
		if binOn {
			name = "bin on"
		}
		t.Run(name, func(t *testing.T) {
			im, lib, bin, dl, theirs := heatRig(t, binOn)
			res, err := im.ImportAsNamed(MovieTrack{Title: "Heat", Year: 1995}, dl)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(lib, "Heat (1995)", "Heat (1995) - 1080p BluRay - Version.mkv"); res.TargetPath != want {
				t.Fatalf("target %q, want %q", res.TargetPath, want)
			}
			if !bytes.Contains(readBytes(t, theirs), []byte("THEIRS")) {
				t.Fatal("the untracked file was replaced")
			}
			if !bytes.Contains(readBytes(t, res.TargetPath), []byte("NEW")) {
				t.Fatal("the new file isn't the download")
			}
			if bin != "" {
				if entries, _ := os.ReadDir(bin); len(entries) != 0 {
					t.Fatalf("something was recycled: %v", entries)
				}
			}
		})
	}
}

// The track's own recorded file under the same name is an upgrade's to replace, through
// the bin, as always.
func TestImportAsNamedReplacesOwnFile(t *testing.T) {
	im, _, bin, dl, own := heatRig(t, true)
	res, err := im.ImportAsNamed(MovieTrack{Title: "Heat", Year: 1995, Own: own}, dl)
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetPath != own || !bytes.Contains(readBytes(t, own), []byte("NEW")) {
		t.Fatalf("the upgrade didn't take the track's own name: %s", res.TargetPath)
	}
	if entries, _ := os.ReadDir(bin); len(entries) == 0 {
		t.Fatal("the replaced file isn't in the bin")
	}
}

// The same bytes already at the name — a re-import, or a file adopted where it lies — are
// "already" there: no second copy beside them.
func TestImportAsNamedSameFileIsAlready(t *testing.T) {
	im, lib, bin, dl, _ := heatRig(t, true)
	at := filepath.Join(lib, "Heat (1995)", "Heat (1995) - 1080p BluRay.mkv")
	writeBytes(t, at, readBytes(t, dl)) // a separate copy with the same bytes
	res, err := im.ImportAsNamed(MovieTrack{Title: "Heat", Year: 1995}, dl)
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetPath != at {
		t.Fatalf("target %q, want the identical file's %q", res.TargetPath, at)
	}
	if entries, _ := os.ReadDir(filepath.Dir(at)); len(entries) != 1 {
		t.Fatalf("a second copy was made: %v", entries)
	}
	if entries, _ := os.ReadDir(bin); len(entries) != 0 {
		t.Fatalf("something was recycled: %v", entries)
	}

	// The file itself, imported where it lies (a manual import from the library folder).
	res, err = im.ImportAsNamed(MovieTrack{Title: "Heat", Year: 1995}, at)
	if err != nil || res.TargetPath != at {
		t.Fatalf("adopting the file in place: %v, %v", res, err)
	}
}

// Untracked files at the label name too: the counter goes on past them.
func TestImportAsNamedSkipsEveryUntrackedName(t *testing.T) {
	im, lib, _, dl, _ := heatRig(t, true)
	dir := filepath.Join(lib, "Heat (1995)")
	second := filepath.Join(dir, "Heat (1995) - 1080p BluRay - Version.mkv")
	writeBytes(t, second, patterned(4096, 's', 10, "SECOND"))
	res, err := im.ImportAsNamed(MovieTrack{Title: "Heat", Year: 1995}, dl)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "Heat (1995) - 1080p BluRay - Version (2).mkv"); res.TargetPath != want {
		t.Fatalf("target %q, want %q", res.TargetPath, want)
	}
	if !bytes.Contains(readBytes(t, second), []byte("SECOND")) {
		t.Fatal("the second untracked file was replaced")
	}
}

// An import no library movie was matched to takes the same care.
func TestImportUnmatchedMovieKeepsUntrackedFile(t *testing.T) {
	im, lib, bin, dl, theirs := heatRig(t, true)
	res, err := im.Import(heatRelease, dl)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(lib, "Heat (1995)", "Heat (1995) - 1080p BluRay - Version.mkv"); res.TargetPath != want {
		t.Fatalf("target %q, want %q", res.TargetPath, want)
	}
	if !bytes.Contains(readBytes(t, theirs), []byte("THEIRS")) {
		t.Fatal("the untracked file was replaced")
	}
	if entries, _ := os.ReadDir(bin); len(entries) != 0 {
		t.Fatalf("something was recycled: %v", entries)
	}
}
