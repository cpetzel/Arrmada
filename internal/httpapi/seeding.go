package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/tristenlammi/arrmada/internal/automation"
)

// seedDefault is the saved global seed goal (Settings → Downloads).
func (a *api) seedDefault(ctx context.Context) automation.SeedDefault {
	if a.deps.Settings == nil {
		return automation.SeedDefault{}
	}
	return automation.ParseSeedDefault(
		a.deps.Settings.Get(ctx, automation.KeySeedRatio, ""),
		a.deps.Settings.Get(ctx, automation.KeySeedHours, ""))
}

// seedGoalProblem says what is wrong with a seed ratio or time, or "" when nothing is (or
// neither was sent). The same bounds hold for the global goal and an indexer's: a negative
// goal would count as met the moment a torrent finished, and removing it then is a
// hit-and-run on a private tracker.
func seedGoalProblem(ratio *float64, hours *int) string {
	if ratio != nil && !automation.ValidSeedRatio(*ratio) {
		return "the seed ratio must be between 0 (none) and " + strconv.FormatFloat(automation.MaxSeedRatio, 'f', -1, 64)
	}
	if hours != nil && !automation.ValidSeedHours(*hours) {
		return "the seeding time must be between 0 (none) and " + strconv.Itoa(automation.MaxSeedHours) + " hours (five years)"
	}
	return ""
}

// handleSeedCheck is the read-only seed check (Settings → Downloads → Seeding): every
// finished torrent with its ratio and seeding time against the goal that applies to it,
// where that goal comes from, and what the next seed check will do with it. ?ratio= and
// ?hours= stand in for the saved global goal, so the page can show what a value would do
// before it is saved. Nothing is changed.
func (a *api) handleSeedCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.deps.Automation == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"torrents": []automation.SeedCheckRow{}})
		return
	}
	var def *automation.SeedDefault
	q := r.URL.Query()
	if q.Has("ratio") || q.Has("hours") {
		d := a.seedDefault(ctx)
		if q.Has("ratio") {
			v, err := strconv.ParseFloat(strings.TrimSpace(q.Get("ratio")), 64)
			if err != nil {
				a.writeError(w, http.StatusBadRequest, "ratio must be a number")
				return
			}
			d.Ratio = v
		}
		if q.Has("hours") {
			v, err := strconv.Atoi(strings.TrimSpace(q.Get("hours")))
			if err != nil {
				a.writeError(w, http.StatusBadRequest, "hours must be a whole number")
				return
			}
			d.Hours = v
		}
		if msg := seedGoalProblem(&d.Ratio, &d.Hours); msg != "" {
			a.writeError(w, http.StatusBadRequest, msg)
			return
		}
		def = &d
	}
	rows, err := a.deps.Automation.SeedCheckQueue(ctx, def)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "could not read the download clients")
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"torrents": rows})
}
