package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/config"
	"github.com/tristenlammi/arrmada/internal/libroots"
	"github.com/tristenlammi/arrmada/internal/pathguard"
)

// Manual import used to walk whatever ?path= it was given, so "/" listed every big
// video, ebook and audio file on every disk of the host. Imports are now confined to
// the folders Arrmada was actually given: downloads and the libraries.
var (
	errImportPathDataDir = errors.New("That's Arrmada's own data folder — pick a folder inside your downloads or library folders")
	errImportPathOutside = errors.New("Pick a folder inside your downloads or library folders (Settings → Library)")
)

// importRoots lists every folder a manual import may read from: downloads and each
// library, as they resolve now. ARRMADA_LIBRARY_DIR isn't one of them: on a documented
// install it's the managed Docker volume, not a share anyone picked.
func (a *api) importRoots(ctx context.Context) []string {
	var roots []string
	rs := a.roots()
	for _, r := range rs.All(ctx) {
		roots = append(roots, r.Path)
	}
	// A library folder's downloads folder that was cleared, replaced or removed with its
	// folder: the torrents already saved there keep seeding there, and Needs you and
	// Import into… must still import them. Each was vetted when it was set.
	return append(roots, rs.FormerDownloads(ctx)...)
}

// importListTimeout bounds one manual-import listing. The walks also stop at
// library.ListMaxResults files and library.ListMaxVisited entries, and as soon as the
// browser goes away (the request context).
const importListTimeout = 30 * time.Second

// importListContext is the context a listing walks under.
func importListContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), importListTimeout)
}

// writeImportList answers a manual-import listing with what the bounded walk found.
// truncated tells the UI to say "showing the first 500"; a walk that ran out of time
// returns what it had, marked truncated with a note. A browser that has already gone
// gets nothing.
func (a *api) writeImportList(w http.ResponseWriter, r *http.Request, ctx context.Context, kind, dir string, cands any, truncated bool) {
	resp := map[string]any{"path": dir, "candidates": cands, "truncated": truncated}
	if err := ctx.Err(); err != nil {
		if rerr := r.Context().Err(); rerr != nil {
			// No path in the line: it can name a book, and this is about the walk.
			a.deps.Log.Debug("manual import listing stopped: the request ended", "kind", kind, "err", rerr)
			return
		}
		resp["truncated"] = true
		resp["note"] = "This folder took too long to list, so these are the files found in the first 30 seconds — pick a narrower folder."
	}
	a.writeJSON(w, http.StatusOK, resp)
}

// checkImportPath vets a manual-import folder or file before anything touches the disk.
// An empty path means the downloads folder. It returns the cleaned absolute path to use
// from here on — the same spelling the rest of the app knows the roots by, so seeding
// and hardlink decisions that compare against the downloads folder keep working — while
// the check itself runs on the fully resolved path, so a symlink can't smuggle the walk
// out of the roots.
func (a *api) checkImportPath(ctx context.Context, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = a.roots().Downloads(ctx)
	}
	clean, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", errImportPathOutside
	}
	if libroots.InDataDir(clean, a.deps.Config.DataDir) {
		return "", errImportPathDataDir
	}
	if !a.withinImportRoots(ctx, clean) {
		return "", a.outsideError(ctx, clean)
	}
	return clean, nil
}

// withinImportRoots reports whether p is inside a folder manual import may read: the
// downloads and library folders, then the folders an admin mapped for a download client.
// The second list is only read when the first doesn't hold p.
func (a *api) withinImportRoots(ctx context.Context, p string, extra ...string) bool {
	return pathguard.Within(p, append(a.importRoots(ctx), extra...)...) || pathguard.Within(p, a.clientImportRoots(ctx)...)
}

// clientImportRoots is the local side of each enabled download client's path mappings
// (GitHub #70). A second client on a volume of its own saves outside every downloads
// folder Arrmada was given — say /downloads beside the first client's /download, through
// the mapping that tells Arrmada where it sees that client's folder — and its finished
// downloads must still be importable from there. Only where an import may read from
// widens: where the files go in the library is decided exactly as before.
//
// Only folders an admin set up count: a mapping is saved by an admin on the client's
// card. A folder the client merely reports (its default save path, a usenet category's
// folder) is the client's word, and never an import root (see outsideError for how to
// allow one). Arrmada's own downloads folders as a client is sent them need no entry
// here: translated through the client's mappings they land in a downloads folder or in
// one of these. Each must still pass usableClientRoot.
func (a *api) clientImportRoots(ctx context.Context) []string {
	if a.deps.Downloads == nil {
		return nil
	}
	var out []string
	for _, p := range a.deps.Downloads.MappedFolders(ctx) {
		if usableClientRoot(p, a.deps.Config) {
			out = append(out, p)
		}
	}
	return out
}

// outsideError is errImportPathOutside, and — when p is inside a folder a download client
// says it saves into but that no admin mapped — how to allow that folder: a path mapping
// on that client, which only an admin can add.
func (a *api) outsideError(ctx context.Context, p string) error {
	if a.deps.Downloads == nil {
		return errImportPathOutside
	}
	var folders []string
	for _, r := range a.roots().DownloadFolders(ctx) {
		folders = append(folders, r.Path)
	}
	for _, f := range a.deps.Downloads.ClientFolders(ctx, folders) {
		if !f.Reported || !usableClientRoot(f.Path, a.deps.Config) || !pathguard.Within(p, f.Path) {
			continue
		}
		return fmt.Errorf("%w. %s is in %s, a folder %s says it saves into. To import from there, an admin can add a path mapping to %s in Settings → Connections → Download clients: %s as %s names it, %s in Arrmada",
			errImportPathOutside, p, f.Path, f.Client, f.Client, f.Remote, f.Client, f.Path)
	}
	return errImportPathOutside
}

// systemDirs are the container's own folders: no download client's folder is one of them
// or inside one, whatever it's mapped to.
var systemDirs = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var"}

// usableClientRoot reports whether a download client's mapped folder may be read by
// manual import: a full path to a folder that's there (symlinks followed), not a whole
// filesystem, not Arrmada's data folder, inside it or holding it, not the managed library
// volume, inside it or holding it (that isn't an import root either; the library folders
// in it are roots of their own), and not one of the container's system folders.
func usableClientRoot(p string, cfg config.Config) bool {
	if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
		return false
	}
	rp, err := pathguard.Resolve(p)
	if err != nil {
		return false
	}
	if st, err := os.Stat(rp); err != nil || !st.IsDir() {
		return false
	}
	if libroots.UnderDataDir(rp, cfg.DataDir) {
		return false
	}
	if lib := strings.TrimSpace(cfg.LibraryDir); lib != "" && (pathguard.Under(lib, rp) || pathguard.Under(rp, lib)) {
		return false
	}
	slashed := filepath.ToSlash(rp)
	for _, d := range systemDirs {
		if slashed == d || strings.HasPrefix(slashed, d+"/") {
			return false
		}
	}
	return true
}
