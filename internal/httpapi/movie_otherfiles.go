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

// handleAddOtherFileAsVersion records one of a film's other files, where it is, as an
// extra version's file: POST /api/v1/movies/{id}/other-files/version
// {path, version_id?, label?, quality_profile?, edition?}. version_id names an existing
// extra version with no file; without it a new, unmonitored version is added. Nothing on
// disk is moved, renamed or copied.
func (a *api) handleAddOtherFileAsVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Path           string `json:"path"`
		VersionID      int64  `json:"version_id"`
		Label          string `json:"label"`
		QualityProfile string `json:"quality_profile"`
		Edition        string `json:"edition"`
	}
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		a.writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if req.VersionID < 0 {
		a.writeError(w, http.StatusBadRequest, "version_id must be a version id")
		return
	}
	if req.VersionID == 0 {
		if req.QualityProfile == "" && a.deps.Quality != nil {
			req.QualityProfile = a.deps.Quality.DefaultProfile(r.Context(), "movie")
		}
		if req.QualityProfile != "" && a.deps.Automation != nil && !a.deps.Automation.KnownProfile(r.Context(), req.QualityProfile) {
			a.writeError(w, http.StatusBadRequest, "unknown quality profile")
			return
		}
	}
	v, err := a.deps.Movies.AddOtherFileAsVersion(r.Context(), id, req.VersionID, req.Path, req.Label, req.QualityProfile, req.Edition)
	switch {
	case errors.Is(err, movies.ErrNotFound):
		a.writeError(w, http.StatusNotFound, "movie not found")
	case errors.Is(err, movies.ErrVersionNotFound):
		a.writeError(w, http.StatusNotFound, "that version isn't one of this movie's")
	case errors.Is(err, movies.ErrNotOtherFile), errors.Is(err, movies.ErrVersionHasFile):
		a.writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		a.writeError(w, http.StatusInternalServerError, "could not add the file as a version")
	default:
		a.writeJSON(w, http.StatusCreated, v)
	}
}
