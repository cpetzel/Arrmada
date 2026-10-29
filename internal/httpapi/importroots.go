package httpapi

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

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
	list := a.importRootList(ctx)
	roots := make([]string, 0, len(list))
	for _, r := range list {
		roots = append(roots, r.Path)
	}
	return roots
}

// importRootList is importRoots with each folder's name, for the folder browser's top
// level. It is the one list: importRoots is built from it, so the browser can never offer
// a folder an import would then refuse, or the other way round.
func (a *api) importRootList(ctx context.Context) []libroots.Root {
	rs := a.roots()
	roots := rs.All(ctx)
	// A library folder's downloads folder that was cleared, replaced or removed with its
	// folder: the torrents already saved there keep seeding there, and Needs you and
	// Import into… must still import them. Each was vetted when it was set.
	for _, p := range rs.FormerDownloads(ctx) {
		roots = append(roots, libroots.Root{Name: "former-downloads", Path: p})
	}
	return roots
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
// returns what it had, marked truncated with a note. note, when set, is said with the list
// (part of the folder couldn't be read). A browser that has already gone gets nothing.
func (a *api) writeImportList(w http.ResponseWriter, r *http.Request, ctx context.Context, kind, dir string, cands any, truncated bool, note string) {
	resp := map[string]any{"path": dir, "candidates": cands, "truncated": truncated}
	if note != "" {
		resp["note"] = note
	}
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
	if err := a.importGuard(ctx)(clean); err != nil {
		return "", err
	}
	return clean, nil
}

// importGuard is checkImportPath's test for an already cleaned, absolute path, with the
// roots read once: the folder browser and search vet every entry they show with it, and
// reading the roots per entry would mean a settings read for each one. The data folder
// is refused first, then anything that isn't (once fully resolved) inside a root.
func (a *api) importGuard(ctx context.Context) func(clean string) error {
	roots := a.importRoots(ctx)
	dataDir := a.deps.Config.DataDir
	return func(clean string) error {
		if libroots.InDataDir(clean, dataDir) {
			return errImportPathDataDir
		}
		if !pathguard.Within(clean, roots...) {
			return errImportPathOutside
		}
		return nil
	}
}
