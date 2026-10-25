package download

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Where each download client's files land, as Arrmada sees them (GitHub #70). With two
// clients on two volumes — the first saving into the Downloads folder, the second into a
// folder of its own — the disk guard has to judge each download by the volume its own
// client writes to.
//
// A torrent client is sent Arrmada's own folder as every grab's save path, translated
// through its path mappings, so its downloads land in that folder as the client names it:
// mapped back, that's where Arrmada finds them. A client can also say where it saves on
// its own (qBittorrent's default save path, SABnzbd's incomplete, completed and category
// folders): those are asked for over the network, so they're cached.
//
// This is only ever used to measure drives: reading how full a drive is harms nothing,
// whoever named the folder. Nothing here makes a folder readable by manual import.

// ClientFolder is one folder a download client saves into, as Arrmada sees it.
type ClientFolder struct {
	ClientID int64  `json:"client_id"`
	Client   string `json:"client"`
	Path     string `json:"path"`
	// Home is where a download from this client that doesn't report a path of its own yet
	// (a magnet still fetching, a usenet job still downloading) is being written: for a
	// torrent client the Downloads folder as it names it, for a usenet client its
	// incomplete folder (its completed folder when it doesn't say).
	Home bool `json:"home"`
	// Reported is a folder the client itself named (a default save path, a usenet client's
	// folders), rather than one of Arrmada's folders translated through the client's
	// mappings. Nothing has checked Arrmada can see it.
	Reported bool `json:"reported"`
}

// clientFoldersTTL is how long a client's own answer about where it saves is kept. Its
// save folders change when someone edits the client's settings, which is rare; asking on
// every disk-guard pass would be one more request a minute to every client.
const clientFoldersTTL = 10 * time.Minute

// clientFolderTimeout bounds one client's answer, so a client that hangs can't hold up
// the disk guard's pass or the status.
const clientFolderTimeout = 5 * time.Second

// usenetFolderReader is a usenet client that names, in one read, the folder it writes a
// job to while it downloads (incomplete), where finished jobs go (complete) and its
// categories' own folders, as it names them (SABnzbd).
type usenetFolderReader interface {
	UsenetFolders(ctx context.Context, dc Client) (complete, incomplete string, cats map[string]string, err error)
}

// reportedFolders is what one client last said about where it saves, as it names them.
type reportedFolders struct {
	at         time.Time
	key        string // the client's kind, address and login when asked: an edit asks again
	save       string
	incomplete string
	cats       []string
}

type clientFoldersCache struct {
	mu   sync.Mutex
	byID map[int64]reportedFolders
}

// EnabledClientCount is how many download clients are switched on. A switched-off client
// left behind from an old setup doesn't make a setup a two-client one.
func (s *Service) EnabledClientCount(ctx context.Context) (int, error) {
	cs, err := s.repo.ListEnabled(ctx)
	return len(cs), err
}

// ClientFolders lists, per enabled client, the folders it saves into as Arrmada sees
// them: each of Arrmada's downloads folders (folders, the Downloads folder first) as a
// torrent client is sent it, then whatever the client itself says it saves into
// (Reported). Each path is absolute and cleaned, and listed once per client; nothing here
// checks it exists. A client that can't be asked right now keeps its last answer.
func (s *Service) ClientFolders(ctx context.Context, folders []string) []ClientFolder {
	clients, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return nil
	}
	var out []ClientFolder
	for _, c := range clients {
		seen := map[string]bool{}
		add := func(p string, home, reported bool) {
			p = strings.TrimSpace(p)
			if p == "" || !filepath.IsAbs(p) {
				return
			}
			p = filepath.Clean(p)
			if seen[p] {
				return
			}
			seen[p] = true
			out = append(out, ClientFolder{ClientID: c.ID, Client: c.Name, Path: p, Home: home, Reported: reported})
		}
		reported := func(remote string, home bool) {
			if remote = strings.TrimSpace(remote); remote != "" {
				add(c.PathMaps.ToLocal(remote), home, true)
			}
		}
		torrent := c.Kind.Transport() == TransportTorrent
		if torrent {
			for i, d := range folders {
				if strings.TrimSpace(d) == "" {
					continue
				}
				// The save path a grab is sent with, then back the way the client reports it.
				add(c.PathMaps.ToLocal(c.PathMaps.ToRemote(d)), i == 0, false)
			}
		}
		rep := s.reportedFolders(ctx, c)
		if !torrent {
			// A usenet job is written to the incomplete folder while it downloads.
			if inc := c.PathMaps.ToLocal(rep.incomplete); strings.TrimSpace(inc) != "" && filepath.IsAbs(inc) {
				reported(rep.incomplete, true)
			} else {
				reported(rep.save, true)
			}
		}
		reported(rep.save, false)
		for _, f := range rep.cats {
			reported(f, false)
		}
	}
	return out
}

// reportedFolders is what client c says about where it saves, cached (clientFoldersTTL).
// A failed ask keeps the previous answer, and isn't repeated before the TTL is up either.
func (s *Service) reportedFolders(ctx context.Context, c Client) reportedFolders {
	impl, ok := s.registry.For(c.Kind)
	if !ok {
		return reportedFolders{}
	}
	ufr, isUsenet := impl.(usenetFolderReader)
	spr, canSave := impl.(savePathReader)
	if !isUsenet && !canSave {
		return reportedFolders{}
	}
	key := string(c.Kind) + "\x00" + c.URL + "\x00" + c.Username
	s.clientFolders.mu.Lock()
	prev, had := s.clientFolders.byID[c.ID]
	s.clientFolders.mu.Unlock()
	if had && prev.key == key && time.Since(prev.at) < clientFoldersTTL {
		return prev
	}
	if !had || prev.key != key {
		prev = reportedFolders{}
	}
	next := reportedFolders{at: time.Now(), key: key, save: prev.save, incomplete: prev.incomplete, cats: prev.cats}
	cctx, cancel := context.WithTimeout(ctx, clientFolderTimeout)
	defer cancel()
	switch {
	case isUsenet:
		complete, incomplete, cats, err := ufr.UsenetFolders(cctx, c)
		if err != nil {
			s.log.Debug("couldn't ask a download client where it saves", "client", c.Name, "err", err)
			break
		}
		next.save, next.incomplete, next.cats = complete, incomplete, nil
		for _, f := range cats {
			next.cats = append(next.cats, f)
		}
		sort.Strings(next.cats)
	case canSave:
		if p, err := spr.DefaultSavePath(cctx, c); err == nil {
			next.save = p
		} else {
			s.log.Debug("couldn't ask a download client where it saves", "client", c.Name, "err", err)
		}
	}
	s.clientFolders.mu.Lock()
	if s.clientFolders.byID == nil {
		s.clientFolders.byID = map[int64]reportedFolders{}
	}
	s.clientFolders.byID[c.ID] = next
	s.clientFolders.mu.Unlock()
	return next
}
