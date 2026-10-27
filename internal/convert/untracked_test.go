package convert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tristenlammi/arrmada/internal/series"
)

// A converted MP4 lands as <stem>.mkv. A video already at that name that no record owns is
// the user's own (a remux kept by hand beside the film): moving it into the originals hold
// had it deleted for good with the original when the hold was released. It's never moved;
// the conversion waits, and both files stay exactly as they were.
func TestSwapNeverHoldsAnUntrackedVideo(t *testing.T) {
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	theirs := finalPathFor(src)
	writeFile(t, theirs, 700, 'r')

	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if kind, _, _ := r.skipOf(t, movieKey(1)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(theirs) != 700 || sizeOf(src) != 1000 || exists(partPathFor(src)) {
		t.Fatalf("a file was moved: theirs %d, original %d, part left %v", sizeOf(theirs), sizeOf(src), exists(partPathFor(src)))
	}
	if r.moviePath(t, 1) != src {
		t.Fatalf("the record moved to %s", r.moviePath(t, 1))
	}
	if held := r.ledger(t, job).HoldPath; held != "" {
		t.Fatalf("something went to the hold: %s", held)
	}
}

// An empty file at the name holds nothing to lose: it's moved aside with the original as
// before, and the conversion lands.
func TestSwapStillMovesAnEmptyStrayAside(t *testing.T) {
	r, src, dst, mi, plan := finalizeRigFor(t, "Heat (1995).mp4")
	stray := finalPathFor(src)
	writeFile(t, stray, 0, 'r')

	job := r.convertNow(t, 1, "Heat", src, dst, mi, plan)
	if job.State != StateDone {
		t.Fatalf("state %s (%s), want done", job.State, job.Note)
	}
	if sizeOf(stray) != 500 || r.moviePath(t, 1) != stray {
		t.Fatalf("the converted file isn't in place: %d bytes, record %s", sizeOf(stray), r.moviePath(t, 1))
	}
	held := r.ledger(t, job).HoldPath
	if fi, err := os.Stat(filepath.Join(filepath.Dir(held), filepath.Base(stray))); err != nil || fi.Size() != 0 {
		t.Fatalf("the empty stray should wait in the hold: %v", err)
	}
}

// The same file is seen before anything is encoded: hours of work aren't spent on a result
// that couldn't be swapped in.
func TestUntrackedVideoAtNameIsSeenBeforeEncoding(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	src := r.addMovieFile(t, 1, "Heat (1995).mp4", 1000)
	theirs := finalPathFor(src)
	writeFile(t, theirs, 700, 'r')
	stubProbe(t, film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8)))
	stubDisks(t, r.scratch, 1<<50, 1<<50)

	it, _ := parseKey(movieKey(1))
	job := r.claim(it, "Heat", true)
	r.process(ctx, job)
	if kind, _, _ := r.skipOf(t, movieKey(1)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if strings.Contains(strings.Join(logMsgs(r.Service), "\n"), "Encoding") {
		t.Fatal("an encode started for a file whose result couldn't be swapped in")
	}
	if sizeOf(theirs) != 700 || sizeOf(src) != 1000 {
		t.Fatal("a file was touched")
	}
}

// Episodes take the same path: an untracked .mkv beside an .mp4 episode stays put.
func TestEpisodeSwapNeverHoldsAnUntrackedVideo(t *testing.T) {
	ctx := context.Background()
	r := newSpaceRig(t)
	r.series = series.NewService(r.db, nil, r.lib, r.log)
	src := filepath.Join(r.lib, "Show", "Show - S01E01.mp4")
	writeFile(t, src, 1000, 'x')
	for _, q := range []string{
		`INSERT INTO series (id, tmdb_id, title, monitored) VALUES (1, 7, 'Show', 1)`,
		`INSERT INTO seasons (series_id, season_number) VALUES (1, 1)`,
	} {
		if _, err := r.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`INSERT INTO episodes (series_id, season_number, episode_number, absolute_number, monitored, has_file, file_path, size_bytes)
		VALUES (1, 1, 1, 1, 1, 1, ?, 1000)`, src); err != nil {
		t.Fatal(err)
	}
	theirs := finalPathFor(src)
	writeFile(t, theirs, 700, 'r')
	mi := film("h264", 1920, 1080, 35000, aud("truehd", "eng", 8))
	mi.SizeBytes = 1000
	plan := Plan{VideoCodec: "hevc"}
	stubProbe(t, &MediaInfo{VideoCodec: "hevc", DurationSec: mi.DurationSec, AudioTracks: 1})
	stubDisks(t, r.scratch, 1<<50, 1<<50)
	dst := filepath.Join(r.scratch, "convert-1.mkv")
	writeFile(t, dst, 500, 'y')
	it, _ := parseKey(episodeKey(1, 1, 1))
	job := r.claim(it, "Show - S01E01", true)
	rec := r.record(job)
	rec.srcPath, rec.srcInfo = src, mi
	rec.setPlan(plan)
	r.beginHistory(job)
	r.finalizeOutput(ctx, job, src, dst, mi, plan)
	if kind, _, _ := r.skipOf(t, episodeKey(1, 1, 1)); job.State != StateSkipped || kind != SkipNameTaken {
		t.Fatalf("state %s, skip %q (%s)", job.State, kind, job.Note)
	}
	if sizeOf(theirs) != 700 || sizeOf(src) != 1000 {
		t.Fatal("a file was moved")
	}
}
