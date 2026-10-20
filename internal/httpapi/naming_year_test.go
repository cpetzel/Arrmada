package httpapi

import (
	"net/http"
	"testing"

	"github.com/tristenlammi/arrmada/internal/auth"
)

// "Add the year only when it's needed" is off until someone turns it on, and a manager —
// who runs naming day to day — can turn it on and off again.
func TestSeriesYearWhenNeededSetting(t *testing.T) {
	a := settingsAPI(t)
	if _, got := getSettings(t, a); got["naming_series_year_when_needed"] != false {
		t.Fatalf("default = %v, want false", got["naming_series_year_when_needed"])
	}
	for _, on := range []bool{true, false} {
		body := `{"naming_series_year_when_needed":false}`
		if on {
			body = `{"naming_series_year_when_needed":true}`
		}
		if w := putSettingsAs(t, a, auth.RoleManager, body); w.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d: %s", body, w.Code, w.Body.String())
		}
		if _, got := getSettings(t, a); got["naming_series_year_when_needed"] != on {
			t.Errorf("after PUT %s it reads %v", body, got["naming_series_year_when_needed"])
		}
	}
	// Leaving it out of a save leaves it as it is.
	putSettingsAs(t, a, auth.RoleManager, `{"naming_series_year_when_needed":true}`)
	if w := putSettingsAs(t, a, auth.RoleManager, `{"write_nfo":true}`); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if _, got := getSettings(t, a); got["naming_series_year_when_needed"] != true {
		t.Error("an unrelated save switched it off")
	}
}
