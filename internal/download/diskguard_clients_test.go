package download

import (
	"context"
	"sort"
	"testing"

	"github.com/tristenlammi/arrmada/internal/diskspace"
)

// perClient is a Downloader that keeps each configured client's torrents apart (by the
// client's name), so a test can have two clients of one kind with different queues.
type perClient map[string]*fakeClient

func (p perClient) of(dc Client) *fakeClient {
	if f, ok := p[dc.Name]; ok {
		return f
	}
	return &fakeClient{}
}
func (p perClient) List(ctx context.Context, dc Client) ([]Item, error) {
	return p.of(dc).List(ctx, dc)
}
func (p perClient) Pause(ctx context.Context, dc Client, hash string) error {
	return p.of(dc).Pause(ctx, dc, hash)
}
func (p perClient) Resume(ctx context.Context, dc Client, hash string) error {
	return p.of(dc).Resume(ctx, dc, hash)
}
func (p perClient) Add(context.Context, Client, AddRequest) error               { return nil }
func (p perClient) Remove(context.Context, Client, string, bool) error          { return nil }
func (p perClient) TorrentAction(context.Context, Client, string, string) error { return nil }
func (p perClient) Test(context.Context, Client) error                          { return nil }

// savingFake is a client that says where it saves (as SABnzbd does: its completed folder).
type savingFake struct {
	*fakeClient
	save  string
	reads int
}

func (s *savingFake) DefaultSavePath(context.Context, Client) (string, error) {
	s.reads++
	return s.save, nil
}

// twoClientGuard is GitHub #70's setup: the first qBittorrent saves into the Downloads
// folder (/download); a second one has its own volume, which it calls /download too
// (Arrmada pins every grab's save path) but Arrmada sees at /downloads, through a path
// mapping on that client.
func twoClientGuard(t *testing.T, first, second []Item) (*DiskGuard, *fakeClient, *fakeClient, context.Context) {
	t.Helper()
	g, _, _, ctx := guardFixture(t, nil)
	a, b := &fakeClient{items: first}, &fakeClient{items: second}
	g.svc.registry.impls[KindQbittorrent] = perClient{"test": a, "second": b}
	if _, err := g.svc.repo.Create(ctx, Client{
		Name: "second", Kind: KindQbittorrent, URL: "http://y", Enabled: true,
		PathMaps: PathMaps{{Remote: "/download", Local: "/downloads"}},
	}); err != nil {
		t.Fatal(err)
	}
	g.SetDirFunc(func() string { return "/download" })
	return g, a, b, ctx
}

// GitHub #70: only the first client's volume is over the line, so only its downloads are
// held — the second client writes to a volume of its own that has room. When that one
// fills instead, its downloads are held (a magnet with no path yet counts as where its
// own client saves), and each is released when its own volume drains.
func TestGuardHoldsOnlyTheFullClientsDownloads(t *testing.T) {
	g, a, b, ctx := twoClientGuard(t,
		[]Item{{Hash: "a1", Name: "First client", State: "downloading", ContentPath: "/download/a1"}},
		[]Item{
			{Hash: "b1", Name: "Second client", State: "downloading", ContentPath: "/download/b1"},
			{Hash: "b2", Name: "Second client magnet", State: "downloading"},
		})
	used := map[string]float64{"/download": 90, "/downloads": 40}
	g.usage = func(p string) (diskspace.Usage, bool) {
		u, ok := used[p]
		return diskspace.Usage{UsedPct: u}, ok
	}

	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(a.paused, "a1") || len(b.paused) != 0 {
		t.Fatalf("paused first %v, second %v — want only the first client's download", a.paused, b.paused)
	}
	st := g.Status(ctx)
	if len(st.Volumes) != 2 || st.Path != "/download" {
		t.Fatalf("status = %+v, want both volumes listed, the full one leading", st)
	}
	clients := map[string][]string{}
	for _, v := range st.Volumes {
		clients[v.Path] = v.Clients
	}
	if c := clients["/downloads"]; len(c) != 1 || c[0] != "second" {
		t.Errorf("the second client's volume names %v, want [second]", c)
	}
	if c := clients["/download"]; len(c) != 1 || c[0] != "test" {
		t.Errorf("the Downloads folder's volume names %v, want [test]", c)
	}

	// The second client's volume fills; the first drains.
	used["/download"], used["/downloads"] = 60, 92
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(a.resumed, "a1") {
		t.Errorf("resumed first %v, want its download back", a.resumed)
	}
	if !sameSet(b.paused, "b1", "b2") {
		t.Errorf("paused second %v, want both of its downloads", b.paused)
	}
	used["/downloads"] = 50
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(b.resumed, "b1", "b2") || len(g.held(ctx)) != 0 {
		t.Errorf("resumed second %v, held %v — want everything released", b.resumed, g.held(ctx))
	}
}

// With two clients, a download whose drive the guard can't tell — its path is in none of
// the folders it knows, or it has no path and its client doesn't say where it saves — is
// left running when some other drive is full, and named in the log once, not every pass.
func TestGuardLeavesAnUnplaceableDownloadAlone(t *testing.T) {
	g, a, _, ctx := twoClientGuard(t, []Item{
		{Hash: "a1", State: "downloading", ContentPath: "/download/a1"},
		{Hash: "elsewhere", Name: "Saved somewhere else", State: "downloading", ContentPath: "/mnt/other/x"},
	}, nil)
	sab := &savingFake{fakeClient: &fakeClient{items: []Item{{Hash: "sab-job", Name: "Usenet job", State: "downloading", RawState: "Downloading"}}}}
	g.svc.registry.impls[KindSABnzbd] = sab // says nothing: save is ""
	if _, err := g.svc.repo.Create(ctx, Client{Name: "sab", Kind: KindSABnzbd, URL: "http://sab", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	g.usage = func(p string) (diskspace.Usage, bool) { return diskspace.Usage{UsedPct: 95}, true }
	var logged []string
	g.noteUnknown = func(it Item) { logged = append(logged, it.Hash) }

	for range 3 {
		if err := g.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !sameSet(a.paused, "a1") || len(sab.paused) != 0 {
		t.Fatalf("paused %v and %v, want only the download on the Downloads folder", a.paused, sab.paused)
	}
	sort.Strings(logged)
	if len(logged) != 2 || logged[0] != "elsewhere" || logged[1] != "sab-job" {
		t.Errorf("logged %v, want each unplaceable download once", logged)
	}
	// Once its client says where it saves, the usenet job is judged by that drive.
	sab.save = "/usenet/complete"
	g.svc.clientFolders.reset()
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(sab.paused, "sab-job") {
		t.Errorf("usenet client paused %v, want its job once its folder is known", sab.paused)
	}
}

// The common case — one download client — is exactly as before: every download still
// pulling data is held while the Downloads folder's volume is full, whatever path it
// reports, and the client is never asked where it saves.
func TestGuardWithOneClientIsUnchanged(t *testing.T) {
	g, _, _, ctx := guardFixture(t, nil)
	one := &savingFake{fakeClient: &fakeClient{items: []Item{
		{Hash: "in", State: "downloading", ContentPath: "/download/in"},
		{Hash: "out", State: "downloading", ContentPath: "/mnt/other/out"},
		{Hash: "nopath", State: "downloading"},
		{Hash: "seed", State: "seeding", ContentPath: "/download/seed"},
	}}, save: "/elsewhere"}
	g.svc.registry.impls[KindQbittorrent] = one
	g.SetDirFunc(func() string { return "/download" })
	used := 90.0
	g.usage = func(string) (diskspace.Usage, bool) { return diskspace.Usage{UsedPct: used}, true }

	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(one.paused, "in", "out", "nopath") {
		t.Fatalf("paused %v, want every download still pulling data", one.paused)
	}
	if st := g.Status(ctx); len(st.Volumes) != 0 || st.Path != "/download" {
		t.Errorf("status = %+v, want the one folder and no volume list", st)
	}
	if one.reads != 0 {
		t.Errorf("asked the only client where it saves %d times", one.reads)
	}
	used = 70
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(one.resumed, "in", "out", "nopath") || len(g.held(ctx)) != 0 {
		t.Errorf("resumed %v, held %v", one.resumed, g.held(ctx))
	}
}

// "Measure a different drive" still stands in for the Downloads folder's filesystem, and
// for a second client's folder on that same filesystem (an Unraid share); a second
// client's folder on a disk of its own keeps its own reading.
func TestGuardOverrideWithTwoClients(t *testing.T) {
	g, a, b, ctx := twoClientGuard(t,
		[]Item{{Hash: "a1", State: "downloading", ContentPath: "/download/a1"}},
		[]Item{{Hash: "b1", State: "downloading", ContentPath: "/download/b1"}})
	_ = g.settings.Set(ctx, KeyDiskGuardMeasure, "/cache")
	disks := &fakeDisks{
		used: map[string]float64{"/download": 50, "/downloads": 50, "/cache": 91},
		dev:  map[string]uint64{"/download": 7, "/downloads": 3, "/cache": 9},
	}
	disks.install(g)
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(a.paused, "a1") || len(b.paused) != 0 {
		t.Fatalf("paused %v and %v, want only the download measured on the pool", a.paused, b.paused)
	}
	// On the same filesystem as the Downloads folder, the second client's folder is
	// measured on the pool too.
	disks.dev["/downloads"] = 7
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(b.paused, "b1") {
		t.Errorf("paused %v, want the second client's download held on the pool's reading", b.paused)
	}
}

// Upgrading with downloads the old guard held for the Downloads folder's volume (nothing
// recorded per download): one that is really on the second client's volume, which has
// room, is released on the first pass; the first client's stays held while its volume
// is full.
func TestGuardReleasesWhatTheOldGuardHeldOnTheWrongVolume(t *testing.T) {
	g, a, b, ctx := twoClientGuard(t,
		[]Item{{Hash: "a1", State: "paused", ContentPath: "/download/a1"}},
		[]Item{{Hash: "b1", State: "paused", ContentPath: "/download/b1"}})
	g.save(ctx, []string{"a1", "b1"})
	used := map[string]float64{"/download": 90, "/downloads": 40}
	g.usage = func(p string) (diskspace.Usage, bool) {
		u, ok := used[p]
		return diskspace.Usage{UsedPct: u}, ok
	}
	if err := g.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !sameSet(b.resumed, "b1") || len(a.resumed) != 0 {
		t.Fatalf("resumed %v and %v, want only the second client's", a.resumed, b.resumed)
	}
	if h := g.held(ctx); len(h) != 1 || h[0] != "a1" {
		t.Errorf("held %v, want the first client's", h)
	}
}
