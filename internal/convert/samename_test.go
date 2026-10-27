package convert

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Only the source under another case is the source's own name. A symlink or a second
// hardlink at the converted file's name is another name: it's taken.
func TestNameIsSourceOnlyForACaseSpelling(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Heat.mp4")
	writeFile(t, src, 10, 'x')
	if !nameIsSource(src, src) {
		t.Fatal("a path isn't its own name")
	}
	link := filepath.Join(dir, "Heat.mkv")
	if err := os.Symlink(src, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if nameIsSource(link, src) {
		t.Fatal("a symlink to the source counted as the source's own name")
	}
	src2 := filepath.Join(dir, "Other.mp4")
	writeFile(t, src2, 10, 'x')
	hard := filepath.Join(dir, "Other.mkv")
	if err := os.Link(src2, hard); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	if nameIsSource(hard, src2) {
		t.Fatal("a second hardlink counted as the source's own name")
	}
}

// ...so the file isn't encoded only to be refused at the swap, again on every retry.
func TestLinkAtNameIsTakenBeforeEncoding(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			r := newSpaceRig(t)
			src := r.addMovieFile(t, 1, "Heat (1995).mp4", 1000)
			final := finalPathFor(src)
			link := os.Symlink
			if kind == "hardlink" {
				link = os.Link
			}
			if err := link(src, final); err != nil {
				t.Skipf("no %s here: %v", kind, err)
			}
			stubProbe(t, film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
			stubDisks(t, r.scratch, 1<<50, 1<<50)
			it, _ := parseKey(movieKey(1))
			job := r.claim(it, "Heat", true)
			r.process(context.Background(), job)
			kindOf, _, _ := r.skipOf(t, movieKey(1))
			if job.State != StateSkipped || (kindOf != SkipNameTaken && kindOf != SkipHardlinked) {
				t.Fatalf("state %s, skip %q (%s)", job.State, kindOf, job.Note)
			}
			if strings.Contains(strings.Join(logMsgs(r.Service), "\n"), "Encoding") {
				t.Fatal("an encode started")
			}
		})
	}
}

// A conversion of Heat (1995).MKV can be reverted on a case-insensitive disk, where the
// original's spelling names the converted file until the original is back.
func TestRevertAfterCaseInsensitiveConversion(t *testing.T) {
	caseInsensitive(t)
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).MKV")
	if err := os.Link(src, finalPathFor(src)); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("conversion: %s (%s)", job.State, job.Note)
	}
	e := r.ledger(t, job)
	if err := os.Link(e.OutPath, e.SrcPath); err != nil { // the disk shows it under both
		t.Fatal(err)
	}
	if _, err := r.Revert(context.Background(), e.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if sizeOf(e.SrcPath) != 1000 || r.moviePath(t, 1) != e.SrcPath {
		t.Fatalf("the original isn't back: %d bytes, record %s", sizeOf(e.SrcPath), r.moviePath(t, 1))
	}
}

// When a swap is put back on a case-insensitive disk, the held original's other spelling
// is the original, not a stray to move back to the converted name.
func TestPutBackNeverTakesTheOriginalForAStray(t *testing.T) {
	caseInsensitive(t)
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).MKV")
	final := finalPathFor(src)
	if err := os.Link(src, final); err != nil {
		t.Skipf("no hardlinks here: %v", err)
	}
	ci := renameFn
	renameFn = func(from, to string) error {
		err := ci(from, to)
		if err == nil && from == src {
			writeFile(t, final, 700, 'r') // someone's file turns up at the name
		}
		return err
	}
	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	renameFn = ci
	if kind, _, _ := r.skipOf(t, movieKey(1)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(src) != 1000 || sizeOf(final) != 700 || r.swapCount(t) != 0 {
		t.Fatalf("original %d, theirs %d, rows %d", sizeOf(src), sizeOf(final), r.swapCount(t))
	}
}

// A swap abandoned at startup after the ledger had marked its original held leaves no held
// row behind: it would count against the hold's size for good.
func TestAbandonedSwapClearsItsHold(t *testing.T) {
	ctx := context.Background()
	r, src, _, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	it, _ := parseKey(movieKey(1))
	job := r.claim(it, "Heat", true)
	rec := r.record(job)
	rec.srcPath, rec.srcInfo = src, mi
	rec.setPlan(plan)
	r.beginHistory(job)
	id := r.record(job).historyID
	held := filepath.Join(r.lib, ".arrmada-hold", strconv.FormatInt(id, 10), filepath.Base(src))
	if err := r.history.setHold(ctx, id, held, time.Now().Add(time.Hour).Unix(), HoldHeld); err != nil {
		t.Fatal(err)
	}
	part := partPathFor(src)
	writeFile(t, part, 500, 'y')
	r.journal(t, swapRow{part: part, final: finalPathFor(src), src: src, movieID: 1, state: swapHeld, holdPath: held, historyID: id})
	r.recoverSwaps(ctx)
	e, err := r.history.get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if e.HoldState == HoldHeld || exists(part) || sizeOf(src) != 1000 {
		t.Fatalf("hold state %q, part left %v, original %d", e.HoldState, exists(part), sizeOf(src))
	}
}

// Recovery that can't put a revert's files back where they were keeps the journal row, so
// the hold isn't released over a library with no file.
func TestRecoveryKeepsARevertItCouldntUndo(t *testing.T) {
	ctx := context.Background()
	r, _, e := convertedFilm(t, "Film.mkv")
	r.journal(t, swapRow{part: revertPartFor(e), final: e.SrcPath, src: e.OutPath, movieID: 1,
		state: swapReverting, holdPath: e.HoldPath, op: opRevert, historyID: e.ID})
	if err := os.Remove(e.OutPath); err != nil { // gone by hand: nothing to move back
		t.Fatal(err)
	}
	r.recoverSwaps(ctx)
	if r.swapCount(t) != 1 {
		t.Fatal("the journal row went, with the converted file nowhere")
	}
	if _, err := r.releaseHold(ctx, e.ID, "test"); err == nil || sizeOf(e.HoldPath) != 1000 {
		t.Fatalf("the held original was released: %v", err)
	}
}

// Startup recovery and a release (the hourly tick runs at start too) take turns on the
// hold: recovery waits for a release in progress.
func TestRecoveryTakesTheHoldLock(t *testing.T) {
	r := newSpaceRig(t)
	r.holdMu.Lock()
	done := make(chan struct{})
	go func() {
		r.recoverSwaps(context.Background())
		close(done)
	}()
	select {
	case <-done:
		r.holdMu.Unlock()
		t.Fatal("recovery ran while the hold was locked")
	case <-time.After(200 * time.Millisecond):
	}
	r.holdMu.Unlock()
	<-done
}
