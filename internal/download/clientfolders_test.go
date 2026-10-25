package download

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/store"
)

func folderService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st.DB(), slog.New(slog.NewTextHandler(io.Discard, nil))), context.Background()
}

// A torrent client's folders are Arrmada's own as it's sent them, mapped back; a usenet
// client's are what it reports (its completed folder is where its jobs land), mapped too.
func TestClientFolders(t *testing.T) {
	s, ctx := folderService(t)
	s.registry.impls[KindQbittorrent] = &fakeClient{}
	sab := &usenetFake{fakeClient: &fakeClient{}, save: "/data/usenet/complete",
		cats: map[string]string{"arrmada-tv": "/data/usenet/tv"}}
	s.registry.impls[KindSABnzbd] = sab
	for _, c := range []Client{
		{Name: "first", Kind: KindQbittorrent, URL: "http://a", Enabled: true},
		{Name: "second", Kind: KindQbittorrent, URL: "http://b", Enabled: true,
			PathMaps: PathMaps{{Remote: "/download", Local: "/downloads"}}},
		// Switched off: none of its folders count.
		{Name: "off", Kind: KindQbittorrent, URL: "http://c", Enabled: false,
			PathMaps: PathMaps{{Remote: "/download", Local: "/off"}}},
		{Name: "sab", Kind: KindSABnzbd, URL: "http://sab", Enabled: true,
			PathMaps: PathMaps{{Remote: "/data/usenet", Local: "/usenet"}}},
	} {
		if _, err := s.repo.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got := s.ClientFolders(ctx, []string{"/download", "/mnt/disk2/dl", ""})
	want := []ClientFolder{
		{Client: "first", Path: "/download", Home: true},
		{Client: "first", Path: "/mnt/disk2/dl"},
		{Client: "second", Path: "/downloads", Home: true},
		{Client: "second", Path: "/mnt/disk2/dl"},
		{Client: "sab", Path: "/usenet/complete", Home: true, Reported: true},
		{Client: "sab", Path: "/usenet/tv", Reported: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		g := got[i]
		g.ClientID = 0
		if g != want[i] {
			t.Errorf("folder %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if n, _ := s.EnabledClientCount(ctx); n != 3 {
		t.Errorf("enabled clients = %d, want 3", n)
	}
}

// SABnzbd's incomplete folder, when it gives one, is where its running jobs are written,
// so that's its home; its completed folder is still one of its folders.
func TestClientFoldersUsenetHomeIsTheIncompleteFolder(t *testing.T) {
	s, ctx := folderService(t)
	s.registry.impls[KindSABnzbd] = &usenetFake{fakeClient: &fakeClient{}, save: "/usenet/complete", inc: "/usenet/incomplete"}
	if _, err := s.repo.Create(ctx, Client{Name: "sab", Kind: KindSABnzbd, URL: "http://sab", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got := s.ClientFolders(ctx, []string{"/download"})
	if len(got) != 2 || got[0].Path != "/usenet/incomplete" || !got[0].Home || got[1].Path != "/usenet/complete" || got[1].Home {
		t.Errorf("folders = %+v", got)
	}
}

// What a client reports is asked for once per clientFoldersTTL; a failed ask keeps the
// last answer and isn't repeated every pass, and an edited client is asked again.
func TestClientFoldersAreCached(t *testing.T) {
	s, ctx := folderService(t)
	sab := &usenetFake{fakeClient: &fakeClient{}, save: "/usenet/complete"}
	s.registry.impls[KindSABnzbd] = sab
	c, err := s.repo.Create(ctx, Client{Name: "sab", Kind: KindSABnzbd, URL: "http://sab", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	home := func() string {
		for _, f := range s.ClientFolders(ctx, []string{"/download"}) {
			if f.Home {
				return f.Path
			}
		}
		return ""
	}
	if h := home(); h != "/usenet/complete" || sab.reads != 1 {
		t.Fatalf("home %q after %d asks", h, sab.reads)
	}
	_ = home()
	if sab.reads != 1 {
		t.Errorf("asked %d times inside the TTL", sab.reads)
	}

	// Past the TTL, with the client failing: the old answer stands, asked once.
	age := func() {
		s.clientFolders.mu.Lock()
		e := s.clientFolders.byID[c.ID]
		e.at = time.Now().Add(-clientFoldersTTL - time.Second)
		s.clientFolders.byID[c.ID] = e
		s.clientFolders.mu.Unlock()
	}
	age()
	sab.err, sab.save = errors.New("down"), ""
	if h := home(); h != "/usenet/complete" || sab.reads != 2 {
		t.Errorf("home %q after %d asks, want the last answer", h, sab.reads)
	}
	_ = home()
	if sab.reads != 2 {
		t.Errorf("a failing client was asked again inside the TTL (%d)", sab.reads)
	}

	// Pointed at another SABnzbd: the old answer is not that one's.
	c.URL = "http://other"
	if err := s.repo.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if h := home(); h != "" || sab.reads != 3 {
		t.Errorf("home %q after %d asks, want nothing known for the new address", h, sab.reads)
	}
}
