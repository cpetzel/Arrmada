package convert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Up to now a file with anything in it at the converted file's name was moved into the
// hold with the original, and releasing the hold removed the whole folder. Users can have
// such a file in a hold right now. Releasing removes only the held original: anything else
// goes back to the library folder, or stays in the hold (said so) when its name is taken.

func legacyHold(t *testing.T) (*spaceRig, HistoryEntry) {
	t.Helper()
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("conversion: %s (%s)", job.State, job.Note)
	}
	return r, r.ledger(t, job)
}

func TestReleaseRestoresAHeldStray(t *testing.T) {
	r, e := legacyHold(t)
	dir := filepath.Dir(e.HoldPath)
	stray := filepath.Join(dir, filepath.Base(e.OutPath))
	writeFile(t, stray, 700, 'r') // what an earlier version moved there
	// The converted file has moved on since.
	if err := os.Remove(e.OutPath); err != nil {
		t.Fatal(err)
	}
	if _, err := r.releaseHold(context.Background(), e.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if sizeOf(e.OutPath) != 700 {
		t.Fatalf("the stray wasn't put back at %s (%d bytes)", e.OutPath, sizeOf(e.OutPath))
	}
	if exists(e.HoldPath) || exists(dir) {
		t.Fatal("the held original or its folder is still there")
	}
}

func TestReleaseKeepsAHeldStrayWhoseNameIsTaken(t *testing.T) {
	r, e := legacyHold(t)
	dir := filepath.Dir(e.HoldPath)
	stray := filepath.Join(dir, filepath.Base(e.OutPath))
	writeFile(t, stray, 700, 'r')
	if _, err := r.releaseHold(context.Background(), e.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if exists(e.HoldPath) {
		t.Fatal("the held original wasn't released")
	}
	if sizeOf(stray) != 700 || sizeOf(e.OutPath) != 500 {
		t.Fatalf("stray %d bytes, converted file %d bytes", sizeOf(stray), sizeOf(e.OutPath))
	}
	if !strings.Contains(strings.Join(logMsgs(r.Service), "\n"), stray) {
		t.Fatal("nothing says where the kept file is")
	}
}

// A revert's converted-* copy left in the folder goes back to the converted file's path.
func TestReleaseRestoresAConvertedCopy(t *testing.T) {
	r, e := legacyHold(t)
	conv := revertPartFor(e)
	if err := os.Rename(e.OutPath, conv); err != nil {
		t.Fatal(err)
	}
	if _, err := r.releaseHold(context.Background(), e.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if sizeOf(e.OutPath) != 500 || exists(conv) {
		t.Fatalf("the converted copy wasn't put back: %d bytes at %s", sizeOf(e.OutPath), e.OutPath)
	}
}

// Empty files and an unfinished cross-drive copy hold nothing to keep: they go with the
// folder, as before.
func TestReleaseStillClearsEmptyLeftovers(t *testing.T) {
	r, e := legacyHold(t)
	dir := filepath.Dir(e.HoldPath)
	writeFile(t, filepath.Join(dir, filepath.Base(e.OutPath)), 0, 'r')
	writeFile(t, e.HoldPath+".partial", 300, 'p')
	if _, err := r.releaseHold(context.Background(), e.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Fatal("the folder of leftovers is still there")
	}
	if sizeOf(e.OutPath) != 500 {
		t.Fatal("the converted file changed")
	}
}

// On a case-insensitive disk Heat (1995).MKV converts to Heat (1995).mkv: the same file.
// That name isn't taken by anyone else. A symlink to the source stands in for it here
// (Linux disks are case-sensitive): the name disappears with the source, as it would.
func TestTargetThatIsTheSourceIsFree(t *testing.T) {
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	final := finalPathFor(src)
	if err := os.Symlink(src, final); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	// On a case-insensitive disk the other spelling goes when the source moves.
	orig := renameFn
	renameFn = func(from, to string) error {
		err := orig(from, to)
		if err == nil && from == src {
			_ = os.Remove(final)
		}
		return err
	}
	t.Cleanup(func() { renameFn = orig })
	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s), want done", job.State, job.Note)
	}
	if sizeOf(final) != 500 || r.moviePath(t, 1) != final || sizeOf(r.ledger(t, job).HoldPath) != 1000 {
		t.Fatal("the conversion didn't land with the original in the hold")
	}

	// And before the encode.
	r2 := newSpaceRig(t)
	src2 := r2.addMovieFile(t, 1, "Heat (1995).mp4", 1000)
	if err := os.Symlink(src2, finalPathFor(src2)); err != nil {
		t.Fatal(err)
	}
	stubProbe(t, film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	stubDisks(t, r2.scratch, 1<<50, 1<<50)
	it, _ := parseKey(movieKey(1))
	job = r2.claim(it, "Heat", true)
	r2.process(context.Background(), job)
	if kind, _, _ := r2.skipOf(t, movieKey(1)); kind == SkipNameTaken {
		t.Fatalf("the source's own name counted as taken (%s)", job.Note)
	}
}

// A revert whose moves fail both ways keeps its journal row: startup recovery puts the
// files right, and the hold can't be released (deleting both) in the meantime.
func TestRevertKeepsItsJournalWhenItCantPutThingsBack(t *testing.T) {
	r, _, e := convertedFilm(t, "Film.mkv")
	conv := revertPartFor(e)
	orig := renameFn
	renameFn = func(from, to string) error {
		if from == e.HoldPath || from == conv {
			return errors.New("injected")
		}
		return orig(from, to)
	}
	t.Cleanup(func() { renameFn = orig })
	if _, err := r.Revert(context.Background(), e.ID); err == nil {
		t.Fatal("the revert reported success")
	}
	if r.swapCount(t) != 1 {
		t.Fatal("the journal row went, though the files are out of place")
	}
	if _, err := r.releaseHold(context.Background(), e.ID, "test"); !errors.Is(err, ErrSwapBusy) {
		t.Fatalf("release while the revert is unsettled: %v", err)
	}
	if sizeOf(e.HoldPath) != 1000 || sizeOf(conv) != 500 {
		t.Fatal("a file was lost")
	}
	renameFn = orig
	r.recoverSwaps(context.Background())
	if sizeOf(e.OutPath) != 500 || r.swapCount(t) != 0 || sizeOf(e.HoldPath) != 1000 {
		t.Fatalf("recovery didn't put the converted file back: %d bytes, %d rows", sizeOf(e.OutPath), r.swapCount(t))
	}
}

// A file that turns up at the name during the swap waits like one that was there before.
func TestStrayAppearingMidSwapIsNameTaken(t *testing.T) {
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	final := finalPathFor(src)
	orig := moveFileFn
	moveFileFn = func(from, to string) error {
		err := orig(from, to)
		writeFile(t, final, 700, 'r') // lands while the encode is being staged
		return err
	}
	t.Cleanup(func() { moveFileFn = orig })
	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if kind, _, _ := r.skipOf(t, movieKey(1)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(final) != 700 || sizeOf(src) != 1000 {
		t.Fatal("a file was moved")
	}
}
