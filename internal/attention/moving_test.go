package attention

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
)

// Imports wait while qBittorrent moves a torrent out of its incomplete folder, so a move
// that never finishes would leave a finished download that never imports and nothing
// saying why. After MovingStuckAfter it's in Needs you as its own kind (not a stall: no
// stall alert, no "another release"; the download is here, the move is what's stuck), and
// it goes once the move finishes.
func TestMovingTorrentNeedsYouAfterTwoHours(t *testing.T) {
	clk := &clock{t: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	moving := download.Item{Hash: "H", Name: "Show.S01.1080p", RawState: "moving", State: "checking", Progress: 1}
	q := []download.Item{moving}
	var mu sync.Mutex
	frame := func(context.Context) *Frame {
		mu.Lock()
		defer mu.Unlock()
		return &Frame{OK: true, Whole: true, Queue: append([]download.Item(nil), q...)}
	}
	s := New(frame, nil, quiet, Downloads())
	s.now = clk.now
	ctx := context.Background()
	refresh := func() *Snapshot {
		t.Helper()
		if err := s.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		return s.Current()
	}
	if c := refresh().Counts; c.Downloads != 0 {
		t.Fatalf("just started moving: %+v", c)
	}
	clk.add(MovingStuckAfter - time.Minute)
	if c := refresh().Counts; c.Downloads != 0 {
		t.Fatalf("a minute short of the limit: %+v", c)
	}
	clk.add(2 * time.Minute)
	snap := refresh()
	if snap.Counts.Downloads != 1 || len(snap.Items) != 1 {
		t.Fatalf("after the limit: %+v", snap)
	}
	it := snap.Items[0]
	if it.Kind != KindMoving || it.Grabbed || !strings.Contains(it.Title, "moving Show.S01.1080p for over 2h") || it.Level != LevelWarning {
		t.Errorf("item = %+v", it)
	}
	// Not a stall: not counted or titled as one, and no "stalled" push.
	if len(snap.Groups) != 1 || strings.Contains(snap.Groups[0].Title, "stalled") {
		t.Errorf("groups = %+v, want one that doesn't say stalled", snap.Groups)
	}
	if ev := eventFor(it); ev != "" {
		t.Errorf("a stuck move raises %q, want no alert", ev)
	}

	// The move finishes: it seeds, and the item goes.
	mu.Lock()
	q[0].RawState, q[0].State = "stalledUP", "seeding"
	mu.Unlock()
	if c := refresh().Counts; c.Downloads != 0 {
		t.Fatalf("after the move finished: %+v", c)
	}
	// A later move starts a fresh clock.
	mu.Lock()
	q[0].RawState, q[0].State = "moving", "checking"
	mu.Unlock()
	clk.add(time.Hour)
	if c := refresh().Counts; c.Downloads != 0 {
		t.Fatalf("a new move, an hour in: %+v", c)
	}
}
