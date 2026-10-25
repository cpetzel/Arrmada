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

// An indexer's seed goal is held to the same bounds. A new indexer that doesn't say gets
// the standard window when there's no global goal (as before) and starts on the global
// goal — its own state, not 0/0 — when there is one.
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
	// A ratio-only global goal isn't given to a new indexer that didn't choose it: a
	// private tracker added now would be ended at that ratio, before its seeding time.
	if err := a.deps.Settings.Set(ctx, automation.KeySeedRatio, "2"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"name":"RatioOnly","kind":"torznab","url":"http://r/api"}`,
		`{"name":"Chosen","kind":"torznab","url":"http://s/api","seed_use_global":true}`,
	} {
		if w := callIndexer(t, a.handleCreateIndexer, http.MethodPost, "/api/v1/indexers", "", body); w.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	if err := a.deps.Settings.Set(ctx, automation.KeySeedHours, "336"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"name":"After","kind":"torznab","url":"http://b/api"}`,
		`{"name":"Own","kind":"torznab","url":"http://c/api","seed_use_global":false,"seed_ratio":3}`,
	} {
		if w := callIndexer(t, a.handleCreateIndexer, http.MethodPost, "/api/v1/indexers", "", body); w.Code != http.StatusCreated {
			t.Fatalf("create %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	got := indexersByName(t, svc)
	if b := got["Before"]; !b.SeedEnabled || b.SeedUseGlobal || b.SeedHours != indexer.DefaultSeedHours || b.SeedRatio != 0 {
		t.Errorf("no global goal: %+v, want the standard window", b)
	}
	if r := got["RatioOnly"]; r.SeedUseGlobal || r.SeedHours != indexer.DefaultSeedHours {
		t.Errorf("ratio-only global goal: %+v, want the standard window", r)
	}
	if c := got["Chosen"]; !c.SeedUseGlobal {
		t.Errorf("an explicit choice of the global goal wasn't kept: %+v", c)
	}
	if af := got["After"]; !af.SeedEnabled || !af.SeedUseGlobal || af.SeedHours != 0 || af.SeedRatio != 0 {
		t.Errorf("with a global goal: %+v, want it on the global goal", af)
	}
	if o := got["Own"]; o.SeedUseGlobal || o.SeedRatio != 3 {
		t.Errorf("own goal chosen: %+v", o)
	}

	id := strconv.FormatInt(got["Before"].ID, 10)
	if w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id, `{"name":"Before","kind":"torznab","url":"http://a/api","seed_enabled":true,"seed_ratio":-2,"seed_hours":336}`); w.Code != http.StatusBadRequest {
		t.Errorf("update with a negative ratio = %d, want 400", w.Code)
	}
	// An edit that leaves seed_use_global out keeps the row's choice.
	aid := strconv.FormatInt(got["After"].ID, 10)
	if w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+aid, aid, `{"name":"After","kind":"torznab","url":"http://b/api","seed_enabled":true,"priority":5}`); w.Code != http.StatusNoContent {
		t.Fatalf("update = %d %s", w.Code, w.Body.String())
	}
	if af := indexersByName(t, svc)["After"]; !af.SeedUseGlobal || af.Priority != 5 {
		t.Errorf("after an edit without seed_use_global: %+v, want still on the global goal", af)
	}
}

// A goal saved before the bounds existed doesn't block an edit that leaves it alone; only
// a changed value is checked.
func TestIndexerUpdateChecksOnlyAChangedSeedGoal(t *testing.T) {
	a, svc := indexerSettingsAPI(t)
	row, err := svc.Create(context.Background(), indexer.Indexer{Name: "Old", Kind: indexer.KindTorznab, URL: "http://o/api", SeedEnabled: true, SeedHours: 50000, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(row.ID, 10)
	if w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id, `{"name":"Renamed","kind":"torznab","url":"http://o/api","seed_enabled":true,"seed_hours":50000}`); w.Code != http.StatusNoContent {
		t.Fatalf("an edit leaving the old goal alone = %d %s, want 204", w.Code, w.Body.String())
	}
	if w := callIndexer(t, a.handleUpdateIndexer, http.MethodPut, "/api/v1/indexers/"+id, id, `{"name":"Renamed","kind":"torznab","url":"http://o/api","seed_enabled":true,"seed_hours":60000}`); w.Code != http.StatusBadRequest {
		t.Errorf("changing it to another out-of-bounds value = %d, want 400", w.Code)
	}
}

func indexersByName(t *testing.T, svc *indexer.Service) map[string]indexer.Indexer {
	t.Helper()
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]indexer.Indexer{}
	for _, ix := range list {
		out[ix.Name] = ix
	}
	return out
}
