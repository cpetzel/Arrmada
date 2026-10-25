package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
	"github.com/tristenlammi/arrmada/internal/automation"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// The global seed goal is unset (0/0) until someone sets it, round-trips, refuses a
// negative or absurd value, and is the admin's to change: it decides when finished
// torrents are removed, data and all.
func TestSettingsGlobalSeedGoal(t *testing.T) {
	a := settingsAPI(t)
	if _, got := getSettings(t, a); got["downloads_seed_ratio"] != float64(0) || got["downloads_seed_hours"] != float64(0) {
		t.Fatalf("defaults = %v / %v, want 0 / 0 (none)", got["downloads_seed_ratio"], got["downloads_seed_hours"])
	}
	if w := putSettings(t, a, `{"downloads_seed_ratio":1.5,"downloads_seed_hours":336}`); w.Code != http.StatusOK {
		t.Fatalf("PUT returned %d: %s", w.Code, w.Body.String())
	}
	if _, got := getSettings(t, a); got["downloads_seed_ratio"] != 1.5 || got["downloads_seed_hours"] != float64(336) {
		t.Errorf("after saving: %v / %v", got["downloads_seed_ratio"], got["downloads_seed_hours"])
	}
	if d := a.seedDefault(context.Background()); d != (automation.SeedDefault{Ratio: 1.5, Hours: 336}) {
		t.Errorf("seedDefault = %+v", d)
	}
	for _, bad := range []string{
		`{"downloads_seed_ratio":-0.5}`, `{"downloads_seed_ratio":101}`,
		`{"downloads_seed_hours":-1}`, `{"downloads_seed_hours":43801}`, `{"downloads_seed_hours":1.5}`,
	} {
		if w := putSettings(t, a, bad); w.Code != http.StatusBadRequest {
			t.Errorf("PUT %s returned %d, want 400", bad, w.Code)
		}
	}
	if _, got := getSettings(t, a); got["downloads_seed_ratio"] != 1.5 {
		t.Errorf("a refused value changed the saved one: %v", got["downloads_seed_ratio"])
	}
	if w := putSettingsAs(t, a, auth.RoleManager, `{"downloads_seed_hours":24}`); w.Code != http.StatusForbidden {
		t.Errorf("manager changing the seed goal = %d, want 403", w.Code)
	}
	// Resending the value in effect is not a change.
	if w := putSettingsAs(t, a, auth.RoleManager, `{"downloads_seed_ratio":1.5,"downloads_seed_hours":336}`); w.Code != http.StatusOK {
		t.Errorf("manager resending the same goal = %d, want 200", w.Code)
	}
}

// indexerSettingsAPI is indexerAPI with a settings service, for the global seed goal.
func indexerSettingsAPI(t *testing.T) (*api, *indexer.Service) {
	t.Helper()
	a, svc := indexerAPI(t)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a.deps.Settings = settings.NewService(st.DB())
	return a, svc
}

// An indexer's seed goal is held to the same bounds; a new indexer without a goal gets the
// standard window when there's no global goal (as before) and keeps 0/0 — "use the global
// goal" — when there is one.
func TestIndexerSeedGoalValidationAndGlobalDefault(t *testing.T) {
	a, svc := indexerSettingsAPI(t)
	ctx := context.Background()
	for _, body := range []string{
		`{"name":"Neg","kind":"torznab","url":"http://a/api","seed_ratio":-1}`,
		`{"name":"Long","kind":"torznab","url":"http://a/api","seed_hours":999999}`,
	} {
		if w := callIndexer(t, a.handleCreateIndexer, http.MethodPost, "/api/v1/indexers", "", body); w.Code != http.StatusBadRequest {
			t.Errorf("create %s = %d, want 400", body, w.Code)
		}
	}

	if w := callIndexer(t, a.handleCreateIndexer, http.MethodPost, "/api/v1/indexers", "", `{"name":"Before","kind":"torznab","url":"http://a/api"}`); w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if err := a.deps.Settings.Set(ctx, automation.KeySeedRatio, "2"); err != nil {
		t.Fatal(err)
	}
	if w := callIndexer(t, a.handleCreateIndexer, http.MethodPost, "/api/v1/indexers", "", `{"name":"After","kind":"torznab","url":"http://b/api"}`); w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]indexer.Indexer{}
	for _, ix := range list {
		got[ix.Name] = ix
	}
	if b := got["Before"]; !b.SeedEnabled || b.SeedHours != indexer.DefaultSeedHours || b.SeedRatio != 0 {
		t.Errorf("no global goal: %+v, want the standard window", b)
	}
	if af := got["After"]; !af.SeedEnabled || af.SeedHours != 0 || af.SeedRatio != 0 {
		t.Errorf("with a global goal: %+v, want 0/0 (uses the global goal)", af)
	}

	id := strconv.FormatInt(got["Before"].ID, 10)
	if w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id, `{"name":"Before","kind":"torznab","url":"http://a/api","seed_ratio":-2}`); w.Code != http.StatusBadRequest {
		t.Errorf("update with a negative ratio = %d, want 400", w.Code)
	}
}
