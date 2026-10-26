package movies

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tristenlammi/arrmada/internal/library"
	"github.com/tristenlammi/arrmada/internal/parser"
)

// Other files are the videos in a film's folder that Arrmada doesn't use: another cut, an
// old copy kept by hand, a second film someone left in the folder. Arrmada only notes
// them — a library scan or a refresh looks, the movies list shows how many, and the film's
// page lists them. Nothing here moves, renames, records or deletes a file, and the page
// offers no action on one: it only says the file is there and not tracked.
//
// What doesn't count, so the list is only what the owner would want to know about:
//   - the film's own files (every track's), and any file another film records;
//   - samples: "sample" in the name or a folder named Sample(s), or under 50 MB;
//   - Plex and Jellyfin local extras: a name ending "-trailer", "-featurette",
//     "-behindthescenes", "-deleted", "-interview", "-scene", "-short", "-other" (or with
//     "." or "_" before it), and anything in an extras folder (Extras, Featurettes,
//     Trailers, Behind The Scenes, Deleted Scenes, Interviews, Scenes, Shorts, Other,
//     Specials);
//   - anything that isn't a video, and Arrmada's own hidden folders.
//
// A folder that isn't provably the film's own (ownFolder, recordedInto: the rules a move
// or a delete uses) may be a collection folder or a whole Movies folder of loose files, so
// other films' files would show up as this one's. There only the folder's own top level
// is looked at, and only files no film in the library records count. A Movies folder
// itself, or a file outside every Movies folder, isn't looked at at all.

// OtherFile is one video in a film's folder that Arrmada doesn't use.
type OtherFile struct {
	Name       string `json:"name"`
	RelPath    string `json:"rel_path"` // from the film's folder, with forward slashes
	Path       string `json:"path"`
	SizeBytes  int64  `json:"size_bytes"`
	Resolution string `json:"resolution,omitempty"` // from the name, or read from the file when the list is opened
	Probed     bool   `json:"probed,omitempty"`     // Resolution was read from the file
}

// OtherFiles is what was found in a film's folder.
type OtherFiles struct {
	Count     int         `json:"count"`
	Files     []OtherFile `json:"files"`
	Folder    string      `json:"folder,omitempty"`
	Shared    bool        `json:"shared,omitempty"`    // not provably the film's own folder (see above)
	Truncated bool        `json:"truncated,omitempty"` // the folder was too big to look through, or Files holds only the first few
	CheckedAt string      `json:"checked_at,omitempty"`
}

const (
	// otherFilesDepth is how many folders below a film's own folder are looked in
	// ("Film (2001)/Cuts/Extended/x.mkv" is two).
	otherFilesDepth = 2
	// otherFilesMaxEntries bounds one folder's look: a film's folder with more entries
	// than this is something else (a whole library in one folder), and stops there.
	otherFilesMaxEntries = 2000
	// otherFilesMaxListed is how many are kept by name; Count goes on counting.
	otherFilesMaxListed = 50
	// otherFilesMaxProbes is how many files without a resolution in the name are read
	// when the list is opened.
	otherFilesMaxProbes = 4
)

// extrasFolderRe is a Plex/Jellyfin local-extras folder, by name.
var extrasFolderRe = regexp.MustCompile(`(?i)^(?:extras?|featurettes?|trailers?|behind the scenes|behindthescenes|deleted scenes|deletedscenes|interviews?|scenes|shorts?|others?|specials?|bonus)$`)

// extrasSuffixes are Plex/Jellyfin local-extras name endings ("Heat (1995)-trailer.mkv").
var extrasSuffixes = []string{"trailer", "featurette", "behindthescenes", "deleted", "deletedscene", "interview", "scene", "short", "other", "clip", "extra"}

// otherFileSkip says why a video at rel (relative to the film's folder, OS separators)
// of size bytes isn't one of the film's other files, or "" when it is one. It judges the
// name alone: the film's own files and other films' records are checked by the caller.
func otherFileSkip(rel string, size int64) string {
	name := filepath.Base(rel)
	if !library.IsVideoFile(name) {
		return "not a video"
	}
	if dir := filepath.Dir(rel); dir != "." {
		for _, part := range strings.Split(dir, string(filepath.Separator)) {
			p := strings.TrimSpace(part)
			if extrasFolderRe.MatchString(p) {
				return "in an extras folder"
			}
			if l := strings.ToLower(p); l == "sample" || l == "samples" {
				return "a sample"
			}
		}
	}
	if library.IsSampleName(name) || size < library.MinEpisodeVideoSize {
		return "a sample"
	}
	stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
	for _, suf := range extrasSuffixes {
		for _, sep := range []string{"-", ".", "_"} {
			if strings.HasSuffix(stem, sep+suf) {
				return "an extra (" + suf + ")"
			}
		}
	}
	return ""
}

// otherFilesPass is what one look through the library shares: every recorded file (so a
// file another film records is never this one's other file), and one linkProbe that
// resolves each of them once.
//
// A scan asks every film's folder whether another film records a file in or under it.
// Looping over every record for every folder was O(films × records) — about 100 million
// comparisons on a 10,000-film library, eating into the scan's time budget. Instead each
// record is filed under every folder above it, once (under; realUnder with links
// resolved), and a folder's question is one map lookup.
//
// Two keys: pathKey (lower-cased) for folder questions, where treating "Heat" and "heat"
// as one folder errs towards "shared" — the careful answer; fileKey (the file system's own
// case rule) for "is this file recorded?", where on Linux "heat (1995).mkv" is not the
// recorded "Heat (1995).mkv" and must be free to be adopted.
type otherFilesPass struct {
	recorded map[string]bool // fileKey of every recorded file
	others   []string        // every recorded file, as recorded
	keys     []string        // pathKey of others[i]
	probe    *linkProbe
	cache    *resolveCache // resolutions kept between passes (nil: none)

	under map[string][]int32 // folder pathKey → the records in or under it, as written

	// Filled once, the first time links matter (index): every record resolved.
	indexed    bool
	real       map[string]bool    // fileKey of every record, links resolved
	realUnder  map[string][]int32 // under, with links resolved
	unresolved []int32            // records that couldn't be resolved

	looked   int // records examined by recordedIntoExcept (tests bound it)
	resolved int // records resolved on disk, not from the cache (tests bound it)
}

// resolveCache keeps each record's resolution (links followed) for a short while, so the
// film page's list, a Refresh and the next look don't each Lstat every recorded path in
// the library again. Only resolutions are kept: which files are recorded is read fresh
// from the database for every pass, so a file recorded a second ago is never missed.
type resolveCache struct {
	mu  sync.Mutex
	ttl time.Duration // 0: resolveTTL
	m   map[string]cachedResolution
}

type cachedResolution struct {
	key string // the cleaned path with links resolved ("" when it couldn't be resolved)
	at  time.Time
}

// resolveTTL is how long a record's resolution is trusted. Links in a library change
// rarely, and a stale answer here only decides whether a folder reads as shared.
const resolveTTL = 2 * time.Minute

func (c *resolveCache) get(path string, now time.Time) (string, bool, bool) {
	if c == nil {
		return "", false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.ttl
	if ttl == 0 {
		ttl = resolveTTL
	}
	r, ok := c.m[path]
	if !ok || now.Sub(r.at) > ttl {
		return "", false, false
	}
	return r.key, r.key != "", true
}

func (c *resolveCache) put(path, key string, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]cachedResolution{}
	}
	if len(c.m) > 200_000 { // a bound, not a policy: start again
		c.m = map[string]cachedResolution{}
	}
	c.m[path] = cachedResolution{key: key, at: now}
}

// newOtherFilesPass reads every recorded file. except, when set, is left out: a film's
// recorded default that rescan knows is gone from disk, so a case-only rename of it
// ("Heat (1995).mkv" → "heat (1995).mkv") isn't mistaken for a recorded file.
func (s *Service) newOtherFilesPass(ctx context.Context, except ...string) (*otherFilesPass, error) {
	rps, err := s.repo.RecordedPaths(ctx)
	if err != nil {
		return nil, err
	}
	p := &otherFilesPass{
		recorded: make(map[string]bool, len(rps)),
		under:    make(map[string][]int32, len(rps)*4),
		probe:    newLinkProbe(nil),
		cache:    &s.resolved,
	}
	skip := map[string]bool{}
	for _, x := range except {
		if x != "" {
			skip[filepath.Clean(x)] = true
		}
	}
	for _, rp := range rps {
		if !skip[filepath.Clean(rp.path)] {
			p.note(rp.path)
		}
	}
	return p, nil
}

// fileUnder files record i under key and every folder above it.
func fileUnder(m map[string][]int32, key string, i int32) {
	for d := key; ; {
		m[d] = append(m[d], i)
		parent := filepath.Dir(d)
		if parent == d {
			return
		}
		d = parent
	}
}

// note adds a recorded file (and, during a scan, one the scan just recorded).
func (p *otherFilesPass) note(path string) {
	if path == "" || p.recorded[fileKey(path)] {
		return
	}
	i := int32(len(p.others))
	p.recorded[fileKey(path)] = true
	p.others = append(p.others, path)
	p.keys = append(p.keys, pathKey(path))
	fileUnder(p.under, pathKey(path), i)
	if p.indexed {
		p.indexOne(i)
	}
}

// index resolves every record once, the first time a question needs links.
func (p *otherFilesPass) index() {
	if p.indexed {
		return
	}
	p.indexed = true
	p.real = make(map[string]bool, len(p.others))
	p.realUnder = make(map[string][]int32, len(p.others)*4)
	for i := range p.others {
		p.indexOne(int32(i))
	}
}

func (p *otherFilesPass) indexOne(i int32) {
	now := time.Now()
	path := p.others[i]
	key, ok, cached := p.cache.get(path, now)
	if !cached {
		p.resolved++
		if ro, err := p.probe.resolvePath(path); err == nil {
			key, ok = filepath.Clean(ro), true
		} else {
			key, ok = "", false
		}
		p.cache.put(path, key, now)
	}
	if !ok {
		p.unresolved = append(p.unresolved, i)
		return
	}
	p.real[fileKey(key)] = true
	fileUnder(p.realUnder, pathKey(key), i)
}

// recordedAs reports whether some film records the file at path: as written, or through
// a link (the two resolve to one place). A path that can't be resolved counts as recorded:
// it's left off the list rather than guessed at.
func (p *otherFilesPass) recordedAs(path string) bool {
	if p.recorded[fileKey(path)] {
		return true
	}
	p.index()
	r, err := p.probe.resolvePath(path)
	return err != nil || p.real[fileKey(r)]
}

// pathKey is how folder questions compare paths: cleaned and lower-cased, so two
// spellings of one folder are never taken for two (refuse rather than guess).
func pathKey(p string) string { return strings.ToLower(filepath.Clean(p)) }

// foldsCase is whether this system's library file systems are usually case-insensitive.
var foldsCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// fileKey is how "is this file recorded?" compares paths: cleaned, and lower-cased only
// where the file system ignores case. On Linux "heat.mkv" beside a recorded "Heat.mkv"
// is another file.
func fileKey(p string) string {
	if foldsCase {
		return pathKey(p)
	}
	return filepath.Clean(p)
}

// firstNotOwn is the first of the records ids that isn't the film's own, or "".
func (p *otherFilesPass) firstNotOwn(ids []int32, own map[string]bool) string {
	for _, i := range ids {
		p.looked++
		if !own[p.keys[i]] {
			return p.others[i]
		}
	}
	return ""
}

// recordedIntoExcept is recordedInto (deletefolder.go) for this pass's records, skipping
// the film's own (own, by pathKey): another film's recorded file that is dir or lies in
// or under it, as written or with every link resolved, or "". A record or a folder that
// can't be resolved counts: refuse rather than guess.
func (p *otherFilesPass) recordedIntoExcept(dir string, own map[string]bool) string {
	clean := filepath.Clean(dir)
	if o := p.firstNotOwn(p.under[pathKey(clean)], own); o != "" {
		return o
	}
	realDir, derr := p.probe.resolvePath(clean)
	if derr != nil {
		for i := range p.others {
			p.looked++
			if !own[p.keys[i]] {
				return p.others[i]
			}
		}
		return ""
	}
	p.index()
	if o := p.firstNotOwn(p.unresolved, own); o != "" {
		return o
	}
	return p.firstNotOwn(p.realUnder[pathKey(realDir)], own)
}

// findOtherFiles looks through movie m's folder (the default file's) for its other files.
// ok is false when the folder isn't looked at (no file, a Movies folder itself, outside
// every Movies folder).
func (s *Service) findOtherFiles(pass *otherFilesPass, m Movie, versions []Version) (OtherFiles, bool) {
	if !m.HasFile || m.MovieFilePath == "" {
		return OtherFiles{}, false
	}
	dir := filepath.Clean(filepath.Dir(m.MovieFilePath))
	if s.looseIn(dir) {
		return OtherFiles{}, false // loose files in a Movies or downloads folder: not the film's folder
	}
	if s.movieRootOf(dir) == "" {
		return OtherFiles{}, false
	}
	own := map[string]bool{pathKey(m.MovieFilePath): true}     // for the folder question
	ownFile := map[string]bool{fileKey(m.MovieFilePath): true} // for "is it the film's?"
	ownPaths := []string{m.MovieFilePath}
	for _, v := range versions {
		if v.FilePath != "" && !ownFile[fileKey(v.FilePath)] {
			own[pathKey(v.FilePath)], ownFile[fileKey(v.FilePath)] = true, true
			ownPaths = append(ownPaths, v.FilePath)
		}
	}
	shared := s.ownFolder(m, dir) != "" || pass.recordedIntoExcept(dir, own) != ""
	depth := otherFilesDepth
	if shared {
		depth = 0
	}
	// The film's own files by identity too: a hardlink of one under another name is the
	// same file, not another one.
	ownInfo := statAll(ownPaths)
	out := OtherFiles{Folder: dir, Shared: shared, Files: []OtherFile{}, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	out.Truncated = s.walkFolderVideos(m, dir, depth, func(full, rel string, fi os.FileInfo) {
		if ownFile[fileKey(full)] || pass.recorded[fileKey(full)] || otherFileSkip(rel, fi.Size()) != "" || sameAsAny(fi, ownInfo) {
			return
		}
		// Last, as it can mean resolving every record once: recorded through a link.
		if pass.recordedAs(full) {
			return
		}
		out.Count++
		if len(out.Files) >= otherFilesMaxListed {
			out.Truncated = true
			return
		}
		name := filepath.Base(full)
		out.Files = append(out.Files, OtherFile{
			Name: name, RelPath: filepath.ToSlash(rel), Path: full, SizeBytes: fi.Size(),
			Resolution: string(parser.Parse(name).StatedResolution()),
		})
	}) || out.Truncated
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].RelPath < out.Files[j].RelPath })
	return out, true
}

// looseIn reports whether dir is itself a Movies, library or downloads folder: a film
// whose file sits loose in one has no folder of its own.
func (s *Service) looseIn(dir string) bool {
	dir = filepath.Clean(dir)
	for _, r := range s.protectedDirs() {
		if strings.EqualFold(filepath.Clean(r), dir) {
			return true
		}
	}
	return false
}

// walkFolderVideos calls fn for every regular video file under dir, down to depth
// folders, with its path relative to dir. Arrmada's hidden folders (the recycle bin,
// Convert's hold) and a system's are left out, and so is a folder named as another film
// ("Alien (1979)/Aliens (1986)"); links aren't followed. It stops after
// otherFilesMaxEntries entries and then reports true.
func (s *Service) walkFolderVideos(m Movie, dir string, depth int, fn func(full, rel string, fi os.FileInfo)) (truncated bool) {
	seen := 0
	var walk func(d, rel string, level int)
	walk = func(d, rel string, level int) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			if truncated {
				return
			}
			if seen++; seen > otherFilesMaxEntries {
				truncated = true
				return
			}
			name := e.Name()
			full, r := filepath.Join(d, name), filepath.Join(rel, name)
			if e.IsDir() {
				if level < depth && !library.SkipScanDir(name) && !s.anotherFilmsFolder(m, name) {
					walk(full, r, level+1)
				}
				continue
			}
			if !e.Type().IsRegular() || !library.IsVideoFile(name) {
				continue
			}
			if fi, err := e.Info(); err == nil {
				fn(full, r, fi)
			}
		}
	}
	walk(dir, "", 0)
	return truncated
}

func statAll(paths []string) []os.FileInfo {
	var out []os.FileInfo
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out = append(out, fi)
		}
	}
	return out
}

func sameAsAny(fi os.FileInfo, infos []os.FileInfo) bool {
	for _, oi := range infos {
		if os.SameFile(fi, oi) {
			return true
		}
	}
	return false
}

// anotherFilmsFolder reports whether a folder called name, inside film m's folder, is
// named as a different film: a title and a year (the parser's reading) that aren't m's.
// "Cuts", "Extended" or "Heat (1995) Extended" are not; "Aliens (1986)" is, in Alien's.
func (s *Service) anotherFilmsFolder(m Movie, name string) bool {
	r := parser.Parse(name)
	return r.Year > 0 && strings.TrimSpace(r.Title) != "" && !s.folderNamedFor(m, name)
}

// refreshOtherFiles looks again at one film's folder and stores what it found (nothing is
// stored, and any old row goes, when the folder isn't looked at). pass may be nil.
func (s *Service) refreshOtherFiles(ctx context.Context, pass *otherFilesPass, m Movie, versions []Version) (OtherFiles, error) {
	if pass == nil {
		var err error
		if pass, err = s.newOtherFilesPass(ctx); err != nil {
			return OtherFiles{}, err
		}
	}
	if versions == nil {
		vs, err := s.repo.ListVersions(ctx, m.ID)
		if err != nil {
			return OtherFiles{}, err
		}
		versions = vs
	}
	of, ok := s.findOtherFiles(pass, m, versions)
	if !ok {
		return OtherFiles{Files: []OtherFile{}}, s.repo.clearOtherFiles(ctx, m.ID)
	}
	return of, s.repo.setOtherFiles(ctx, m.ID, of)
}

// OtherFilesLive is the detail list: the film's folder looked at now (and stored), with
// the resolution read from up to a few files whose names don't say it.
func (s *Service) OtherFilesLive(ctx context.Context, id int64) (OtherFiles, error) {
	m, err := s.repo.Get(ctx, id)
	if err != nil {
		return OtherFiles{}, err
	}
	of, err := s.refreshOtherFiles(ctx, nil, m, nil)
	if err != nil {
		return OtherFiles{}, err
	}
	probes := 0
	for i := range of.Files {
		f := &of.Files[i]
		if f.Resolution != "" || probes >= otherFilesMaxProbes || ctx.Err() != nil {
			continue
		}
		probes++
		if mi, err := s.probeFile(f.Path); err == nil && mi.Resolution != "" {
			f.Resolution, f.Probed = mi.Resolution, true
		}
	}
	return of, nil
}

// StoredOtherFiles is what the last look at the film's folder found (nil when it has no
// file or hasn't been looked at): the detail page's chip, without touching the disk.
func (s *Service) StoredOtherFiles(ctx context.Context, m Movie) *OtherFiles {
	if !m.HasFile {
		return nil
	}
	of, ok, err := s.repo.otherFiles(ctx, m.ID)
	if err != nil || !ok {
		return nil
	}
	of.Folder = filepath.Dir(m.MovieFilePath)
	return &of
}

// noteOtherFilesInScan is the library scan's look at a film's folder, right after the scan
// decided the folder is the film's. A failure is logged; the scan goes on.
func (s *Service) noteOtherFilesInScan(ctx context.Context, pass *otherFilesPass, m Movie, versions []Version) {
	if pass == nil || ctx.Err() != nil {
		return
	}
	if _, err := s.refreshOtherFiles(ctx, pass, m, versions); err != nil {
		s.log.Warn("library scan: couldn't note the other files in a film's folder", "title", m.Title, "err", err)
	}
}

// setOtherFiles stores what a look at a film's folder found. When it's what the stored
// row already says, nothing is written: a scan re-looking at an unchanged library makes
// no writes (checked_at then keeps the time it last changed).
func (r *Repo) setOtherFiles(ctx context.Context, id int64, of OtherFiles) error {
	files := of.Files
	if files == nil {
		files = []OtherFile{}
	}
	b, err := json.Marshal(files)
	if err != nil {
		return err
	}
	var (
		count, shared, truncd int
		stored                string
	)
	err = r.q().QueryRowContext(ctx, `SELECT file_count, files_json, shared, truncated FROM movie_other_files WHERE movie_id = ?`, id).
		Scan(&count, &stored, &shared, &truncd)
	if err == nil && count == of.Count && stored == string(b) && shared == boolToInt(of.Shared) && truncd == boolToInt(of.Truncated) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = r.q().ExecContext(ctx, `INSERT INTO movie_other_files (movie_id, file_count, files_json, shared, truncated, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(movie_id) DO UPDATE SET file_count = excluded.file_count, files_json = excluded.files_json,
			shared = excluded.shared, truncated = excluded.truncated, checked_at = excluded.checked_at`,
		id, of.Count, string(b), boolToInt(of.Shared), boolToInt(of.Truncated), of.CheckedAt)
	return err
}

// clearOtherFiles drops a film's stored look (no write when there's none).
func (r *Repo) clearOtherFiles(ctx context.Context, id int64) error {
	var n int
	if err := r.q().QueryRowContext(ctx, `SELECT COUNT(*) FROM movie_other_files WHERE movie_id = ?`, id).Scan(&n); err != nil || n == 0 {
		return err
	}
	_, err := r.q().ExecContext(ctx, `DELETE FROM movie_other_files WHERE movie_id = ?`, id)
	return err
}

// otherFiles is the stored look at a film's folder (ok false when there's none).
func (r *Repo) otherFiles(ctx context.Context, id int64) (OtherFiles, bool, error) {
	var (
		of             OtherFiles
		filesJSON      string
		shared, truncd int
	)
	err := r.q().QueryRowContext(ctx, `SELECT file_count, files_json, shared, truncated, checked_at
		FROM movie_other_files WHERE movie_id = ?`, id).Scan(&of.Count, &filesJSON, &shared, &truncd, &of.CheckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OtherFiles{}, false, nil
	}
	if err != nil {
		return OtherFiles{}, false, err
	}
	of.Shared, of.Truncated = shared != 0, truncd != 0
	if json.Unmarshal([]byte(filesJSON), &of.Files) != nil || of.Files == nil {
		of.Files = []OtherFile{}
	}
	return of, true, nil
}
