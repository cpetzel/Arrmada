package httpapi

import (
	"errors"
	"net/http"

	"github.com/tristenlammi/arrmada/internal/movies"
)

// handleMovieOtherFiles lists the videos in a film's folder that Arrmada doesn't use,
// looked at now (movies.Service.OtherFilesLive): GET /api/v1/movies/{id}/other-files.
// Read-only; the film's page opens it from its "+N other files" chip.
func (a *api) handleMovieOtherFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	of, err := a.deps.Movies.OtherFilesLive(r.Context(), id)
	if errors.Is(err, movies.ErrNotFound) {
		a.writeError(w, http.StatusNotFound, "movie not found")
		return
	}
	if err != nil {
		a.writeError(w, http.StatusInternalServerError, "could not look in the film's folder")
		return
	}
	a.writeJSON(w, http.StatusOK, of)
}
