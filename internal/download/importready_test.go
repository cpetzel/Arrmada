package download

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// ReadyToImport is Complete, minus what a torrent's files can't be read from yet: a
// qBittorrent move in progress, or files the client says are missing. Complete itself is
// unchanged, so the stall checks and queue pages still see a moving torrent as done.
func TestItemReadyToImport(t *testing.T) {
	done := Item{Progress: 1, RemainingBytes: 0}
	cases := []struct {
		name           string
		it             Item
		complete, want bool
	}{
		{"seeding", inState(done, "stalledUP", ""), true, true},
		{"moving out of the incomplete folder", inState(done, "moving", ""), true, false},
		{"files missing", inState(done, "missingFiles", ""), true, false},
		{"still downloading", Item{Progress: 0.5, RemainingBytes: 10, RawState: "downloading"}, false, false},
		{"moving mid-download", Item{Progress: 0.5, RemainingBytes: 10, RawState: "moving"}, false, false},
		// SABnzbd's own "Moving" is post-processing: such a job isn't Complete until
		// SABnzbd says Completed, and a completed one is never held back by its raw state.
		{"usenet completed", Item{Progress: 1, Transport: TransportUsenet, State: StateCompleted, RawState: "Completed"}, true, true},
		{"usenet moving", Item{Progress: 1, Transport: TransportUsenet, State: PhaseUnpacking, RawState: "Moving"}, false, false},
	}
	for _, tc := range cases {
		if got := tc.it.Complete(); got != tc.complete {
			t.Errorf("%s: Complete = %v, want %v", tc.name, got, tc.complete)
		}
		if got := tc.it.ReadyToImport(); got != tc.want {
			t.Errorf("%s: ReadyToImport = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func inState(it Item, raw, path string) Item {
	it.RawState, it.State, it.ContentPath = raw, normalizeState(raw), path
	return it
}

// A qBittorrent with an incomplete folder: the torrent reaches 100% while still in it, sits
// in "moving" while it's copied to the save folder, then seeds from there. The import
// candidates leave it out until the move is done, and then offer it at its final path —
// never the incomplete folder's.
func TestCompletedInCategoryWaitsForTheMove(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	state, path := "moving", "/incomplete/Show.S01.1080p"
	var logins int32
	srv := qbitTestServer(t, &logins, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/info" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `[{"hash":"aaa","name":"Show.S01.1080p","progress":1.0,"amount_left":0,"state":%q,
			"category":"arrmada-tv","content_path":%q}]`, state, path)
	})
	svc, _ := newTestService(t)
	if _, err := svc.Create(ctx, Client{Name: "qb", Kind: KindQbittorrent, URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.CompletedInCategory(ctx, "arrmada-tv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a torrent qBittorrent is still moving was offered for import: %+v", got)
	}
	// The queue pages still see it as finished: only the importers wait.
	q, err := svc.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || !q[0].Complete() || !q[0].Moving() {
		t.Fatalf("queue = %+v, want the one torrent, complete and moving", q)
	}

	mu.Lock()
	state, path = "stalledUP", "/downloads/tv/Show.S01.1080p"
	mu.Unlock()
	svc.InvalidateSnapshot()
	got, err = svc.CompletedInCategory(ctx, "arrmada-tv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ContentPath != "/downloads/tv/Show.S01.1080p" {
		t.Fatalf("after the move: candidates = %+v, want the torrent at its save folder", got)
	}
}

// The path check's "newest finished download" isn't a torrent mid-move: its content path
// names the incomplete folder, and reading that as a download Arrmada can't see would fail
// the check over a folder imports never read.
func TestNewestFinishedSkipsAMovingTorrent(t *testing.T) {
	items := []Item{
		inState(Item{Hash: "old", Progress: 1, AddedOn: 100}, "stalledUP", "/downloads/Film.2020"),
		inState(Item{Hash: "new", Progress: 1, AddedOn: 200}, "moving", "/incomplete/Show.S01"),
		inState(Item{Hash: "gone", Progress: 1, AddedOn: 300}, "missingFiles", "/downloads/Gone"),
	}
	if got := newestFinished(items); got == nil || got.Hash != "old" {
		t.Fatalf("newestFinished = %+v, want the seeding film", got)
	}
}
